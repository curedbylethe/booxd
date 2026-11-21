package parse

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/curedbylethe/booxd/internal/source"
)

func TestDetailPageFixtures(t *testing.T) {
	tests := []struct {
		name string
		want Detail
	}{
		{
			name: "book-detail.html",
			want: Detail{ID: source.Ref{Source: source.Anna, ID: "36451"}, Title: "Test", Year: "2022", Language: "English", Format: "PDF", Size: "51.0 MB", Pages: "1", LoginWall: true},
		},
		{
			name: "book-detail-rich.html",
			want: Detail{ID: source.Ref{Source: source.Anna, ID: "30208739"}, Title: "Test", Author: "Sleator, William", Publisher: "Amulet Books", Year: "2010", Language: "English", Format: "EPUB", Size: "3.0 MB", ISBN: "9780810993563,9780810989894", LoginWall: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DetailPage(bytes.NewReader(fixture(t, tt.name)))
			if err != nil {
				t.Fatal(err)
			}
			gotDownloads := got.Downloads
			got.Downloads = nil
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("detail: %+v, want %+v", got, tt.want)
			}
			if len(gotDownloads) != 38 {
				t.Fatalf("got %d downloads, want 38", len(gotDownloads))
			}
			for i, download := range gotDownloads {
				if !download.RequiresSession {
					t.Errorf("download %d does not require session", i)
				}
			}
			if gotDownloads[0].Label != "Fast Partner Server #1" || gotDownloads[37].Label != "Slow Partner Server #16" {
				t.Errorf("first/last labels: %q / %q", gotDownloads[0].Label, gotDownloads[37].Label)
			}
		})
	}
}

const (
	descriptionFirst  = "A comprehensive update of the leading algorithms text, with new material on matchings in bipartite graphs, online algorithms, machine learning, and other topics."
	descriptionSecond = "Some books on algorithms are rigorous but incomplete; others cover masses of material but lack rigor. Introduction to Algorithms uniquely combines rigor and comprehensiveness. It covers a broad range of algorithms in depth, yet makes their design and analysis accessible to all levels of readers, with self-contained chapters and algorithms in pseudocode. Since the publication of the first edition, Introduction to Algorithms has become the leading algorithms text in universities worldwide as well as the standard reference for professionals. This fourth edition has been updated throughout."
	descriptionLast   = "New for the fourth edition\n" +
		"• New chapters on matchings in bipartite graphs, online algorithms, and machine learning\n" +
		"• New material on topics including solving recurrence equations, hash tables, potential functions, and suffix arrays\n" +
		"• 140 new exercises and 22 new problems\n" +
		"• Reader feedback–informed improvements to old problems\n" +
		"• Clearer, more personal, and gender-neutral writing style\n" +
		"• Color added to improve visual presentation\n" +
		"• Notes, bibliography, and index updated to reflect developments in the field\n" +
		"• Website with new supplementary material"
)

func TestDetailPageDescription(t *testing.T) {
	tests := []struct {
		name    string
		page    string
		want    string
		first   string
		last    string
		minPara int
	}{
		{
			name:    "fixture",
			page:    string(fixture(t, "book-detail-description.html")),
			want:    descriptionFirst + "\n\n" + descriptionSecond + "\n\n" + descriptionLast,
			first:   descriptionFirst,
			last:    descriptionLast,
			minPara: 3,
		},
		{
			name: "placeholder only",
			page: `<div id="book-panel-details"><table><tr><td>Title</td><td>Test</td></tr></table></div>
			<div id="book-panel-overview"><h3>Record overview</h3>
			<p class="mb-4 text-[#666]">No long-form description is available for this record in the imported catalog yet.</p></div>`,
			want: "",
		},
		{
			name: "placeholder inside prose block",
			page: `<div id="book-panel-details"><table><tr><td>Title</td><td>Test</td></tr></table></div>
			<div id="book-panel-overview"><h3>Record overview</h3>
			<div class="mb-4 prose prose-sm max-w-none">No long-form description is available for this record in the imported catalog yet.</div></div>`,
			want: "",
		},
		{
			name: "paragraph markup with link",
			page: `<div id="book-panel-details"><table><tr><td>Title</td><td>Test</td></tr></table></div>
			<div id="book-panel-overview"><h3>Record overview</h3>
			<div class="mb-4 prose prose-sm max-w-none">
			<p>First paragraph with a <a href="/x">link</a> inside.</p>
			<p>Second   paragraph.</p></div></div>`,
			want: "First paragraph with a link inside.\n\nSecond paragraph.",
		},
		{
			name: "no overview",
			page: `<div id="book-panel-details"><table><tr><td>Title</td><td>Test</td></tr></table></div>`,
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detail, err := DetailPage(strings.NewReader(tt.page))
			if err != nil {
				t.Fatal(err)
			}
			if detail.Description != tt.want {
				t.Fatalf("description: %q, want %q", detail.Description, tt.want)
			}
			if tt.first != "" && !strings.HasPrefix(detail.Description, tt.first+"\n\n") {
				t.Errorf("first paragraph: %q", detail.Description)
			}
			if tt.last != "" && !strings.HasSuffix(detail.Description, tt.last) {
				t.Errorf("last paragraph: %q", detail.Description)
			}
			if n := len(strings.Split(detail.Description, "\n\n")); n < tt.minPara {
				t.Errorf("got %d paragraphs, want at least %d", n, tt.minPara)
			}
		})
	}
}

func TestDetailPageMixedDownloads(t *testing.T) {
	page := `<div id="book-panel-details"><table><tr><td>Record ID</td><td>1</td></tr><tr><td>Title</td><td>Test</td></tr></table></div>
	<div id="book-panel-downloads">Log in to access downloads<ul>
	<li class="list-disc"><span class="font-medium text-[#444]">Fast Partner Server #1</span><a href="https://annas-archive.is/account">Log in to access downloads</a></li>
	<li class="list-disc"><span class="font-medium text-[#444]">Slow Partner Server #1</span><a href="https://annas-archive.is/slow_download/abc/0/0">Download</a></li>
	</ul></div>`
	detail, err := DetailPage(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Downloads) != 2 || !detail.Downloads[0].RequiresSession || detail.Downloads[1].RequiresSession || detail.LoginWall {
		t.Fatalf("detail: %+v", detail)
	}
}

func TestDetailPageWithoutDownloadsPanel(t *testing.T) {
	page := `<div id="book-panel-details"><table><tr><td>Title</td><td>Test</td></tr></table></div>`
	detail, err := DetailPage(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Downloads) != 0 || detail.LoginWall {
		t.Fatalf("detail: %+v", detail)
	}
}

func TestDetailPageRejectsSearchPage(t *testing.T) {
	_, err := DetailPage(bytes.NewReader(fixture(t, "search-hit.html")))
	if err == nil || !strings.Contains(err.Error(), "parse detail: no detail table") {
		t.Fatalf("error: %v", err)
	}
}

func TestDetailPageNormalizesPlaceholders(t *testing.T) {
	page := `<div id="book-panel-details"><table><tr><td>Author</td><td>UNKNOWN AUTHOR</td></tr><tr><td>Pages</td><td>0</td></tr></table></div>`
	detail, err := DetailPage(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	if detail.Author != "" || detail.Pages != "" || detail.Publisher != "" || detail.ISBN != "" {
		t.Fatalf("detail: %+v", detail)
	}
}

func TestDetailPageStripsTerminalControls(t *testing.T) {
	page := `<div id="book-panel-details"><table><tr><td>Title</td><td>Evil` + "\x1b" + `]0;pwned` + "\a\x1b" + `[2J</td></tr></table></div>`
	detail, err := DetailPage(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	if detail.Title != "Evil]0;pwned[2J" {
		t.Fatalf("title retained terminal controls: %q", detail.Title)
	}
}
