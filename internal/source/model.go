package source

// Book is one search result in a provider-neutral form, for display.
type Book struct {
	Ref       Ref
	Title     string
	Author    string
	Year      string
	Format    string
	Size      string
	Publisher string
	Language  string
}

// Detail is one book's record in a provider-neutral form, for display.
type Detail struct {
	Ref         Ref
	Title       string
	Author      string
	Publisher   string
	Year        string
	Language    string
	Format      string
	Size        string
	ISBN        string
	Pages       string
	Description string
	HasDownload bool
}
