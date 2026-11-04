package parse

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestDetailPageFixtures(t *testing.T) {
	tests := []struct {
		name string
		want Detail
	}{
		{
			name: "book-detail.html",
			want: Detail{ID: "36451", Title: "Test", Year: "2022", Language: "English", Format: "PDF", Size: "51.0 MB", Pages: "1", LoginWall: true},
		},
		{
			name: "book-detail-rich.html",
			want: Detail{ID: "30208739", Title: "Test", Author: "Sleator, William", Publisher: "Amulet Books", Year: "2010", Language: "English", Format: "EPUB", Size: "3.0 MB", ISBN: "9780810993563,9780810989894", LoginWall: true},
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
