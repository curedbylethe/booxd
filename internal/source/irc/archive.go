package irc

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
)

const (
	// maxArchiveSize caps one offered archive so a hostile sender cannot
	// exhaust memory on a Boox (mirrors update.maxAssetSize).
	maxArchiveSize = 64 << 20
	// maxMemberSize caps one decompressed member; an archive cannot be larger
	// than the whole payload, so the same bound is enough.
	maxMemberSize = maxArchiveSize
)

// scanArchive reads every member of a zip archive and parses each line.
func scanArchive(data []byte) ([]Book, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("source irc: archive: %w", err)
	}

	books := []Book{}
	for _, file := range reader.File {
		member, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("source irc: archive: open %q: %w", file.Name, err)
		}
		body, err := io.ReadAll(io.LimitReader(member, maxMemberSize+1))
		_ = member.Close()
		if err != nil {
			return nil, fmt.Errorf("source irc: archive: read %q: %w", file.Name, err)
		}
		if len(body) > maxMemberSize {
			return nil, fmt.Errorf("source irc: archive: %q member exceeds %d bytes", file.Name, maxMemberSize)
		}

		for _, line := range bytes.Split(body, []byte("\n")) {
			line = bytes.TrimSuffix(line, []byte("\r"))
			if len(line) == 0 || line[0] != '!' {
				continue
			}
			if book, ok := parseLine(string(line)); ok {
				books = append(books, book)
			}
		}
	}
	return books, nil
}

// readOffer reads an offered archive into memory, bounded, and scans it.
func readOffer(r io.Reader, declared int64) ([]Book, error) {
	limit := int64(maxArchiveSize) + 1
	if declared > 0 && declared+1 < limit {
		limit = declared + 1
	}
	data, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return nil, fmt.Errorf("source irc: archive: %w", err)
	}
	if len(data) > maxArchiveSize {
		return nil, fmt.Errorf("source irc: archive: %d bytes exceeds the %d byte limit", len(data), maxArchiveSize)
	}
	if declared > 0 && int64(len(data)) != declared {
		return nil, fmt.Errorf("source irc: archive: read %d of %d bytes", len(data), declared)
	}
	return scanArchive(data)
}
