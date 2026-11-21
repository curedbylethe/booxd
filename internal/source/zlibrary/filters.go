package zlibrary

import (
	"fmt"
	"strings"
)

// shortcut: the table and the extension set are static, so a language or format
// the host adds later needs a code change. Upgrade when the host's list changes or
// a user reports a rejected value.

// languageNames maps both ISO 639-1 codes and EAPI language names to the EAPI
// name sent on the wire. Keys are matched after trimming and lowercasing.
var languageNames = map[string]string{
	"en": "english", "english": "english",
	"fa": "persian", "persian": "persian",
	"fr": "french", "french": "french",
	"de": "german", "german": "german",
	"es": "spanish", "spanish": "spanish",
	"it": "italian", "italian": "italian",
	"pt": "portuguese", "portuguese": "portuguese",
	"ru": "russian", "russian": "russian",
	"zh": "chinese", "chinese": "chinese",
	"ja": "japanese", "japanese": "japanese",
	"ko": "korean", "korean": "korean",
	"ar": "arabic", "arabic": "arabic",
	"hi": "hindi", "hindi": "hindi",
	"bn": "bengali", "bengali": "bengali",
	"tr": "turkish", "turkish": "turkish",
	"pl": "polish", "polish": "polish",
	"nl": "dutch", "dutch": "dutch",
	"sv": "swedish", "swedish": "swedish",
	"no": "norwegian", "norwegian": "norwegian",
	"da": "danish", "danish": "danish",
	"fi": "finnish", "finnish": "finnish",
	"cs": "czech", "czech": "czech",
	"el": "greek", "greek": "greek",
	"he": "hebrew", "hebrew": "hebrew",
	"hu": "hungarian", "hungarian": "hungarian",
	"id": "indonesian", "indonesian": "indonesian",
	"ro": "romanian", "romanian": "romanian",
	"sk": "slovak", "slovak": "slovak",
	"uk": "ukrainian", "ukrainian": "ukrainian",
	"vi": "vietnamese", "vietnamese": "vietnamese",
	"th": "thai", "thai": "thai",
	"bg": "bulgarian", "bulgarian": "bulgarian",
	"sr": "serbian", "serbian": "serbian",
	"hr": "croatian", "croatian": "croatian",
	"sl": "slovenian", "slovenian": "slovenian",
	"et": "estonian", "estonian": "estonian",
	"lv": "latvian", "latvian": "latvian",
	"lt": "lithuanian", "lithuanian": "lithuanian",
	"ca": "catalan", "catalan": "catalan",
	"ms": "malaysian", "malaysian": "malaysian",
	"ur": "urdu", "urdu": "urdu",
	"sw": "swahili", "swahili": "swahili",
	"ta": "tamil", "tamil": "tamil",
	"te": "telugu", "telugu": "telugu",
	"ml": "malayalam", "malayalam": "malayalam",
	"kn": "kannada", "kannada": "kannada",
	"gu": "gujarati", "gujarati": "gujarati",
	"pa": "punjabi", "punjabi": "punjabi",
	"ne": "nepali", "nepali": "nepali",
	"si": "sinhala", "sinhala": "sinhala",
	"km": "central_khmer", "central_khmer": "central_khmer",
	"lo": "lao", "lao": "lao",
	"my": "burmese", "burmese": "burmese",
	"ka": "georgian", "georgian": "georgian",
	"hy": "armenian", "armenian": "armenian",
	"az": "azerbaijani", "azerbaijani": "azerbaijani",
	"kk": "kazakh", "kazakh": "kazakh",
	"uz": "uzbek", "uzbek": "uzbek",
	"be": "belarusian", "belarusian": "belarusian",
	"mk": "macedonian", "macedonian": "macedonian",
	"sq": "albanian", "albanian": "albanian",
	"bs": "bosnian", "bosnian": "bosnian",
	"is": "icelandic", "icelandic": "icelandic",
	"ga": "irish", "irish": "irish",
	"cy": "welsh", "welsh": "welsh",
	"mt": "maltese", "maltese": "maltese",
	"lb": "luxembourgish", "luxembourgish": "luxembourgish",
	"af": "afrikaans", "afrikaans": "afrikaans",
	"am": "amharic", "amharic": "amharic",
	"yo": "yoruba", "yoruba": "yoruba",
	"ig": "igbo", "igbo": "igbo",
	"ha": "hausa", "hausa": "hausa",
	"zu": "zulu", "zulu": "zulu",
	"xh": "xhosa", "xhosa": "xhosa",
	"so": "somali", "somali": "somali",
	"ps": "pashto", "pashto": "pashto",
	"ku": "kurdish", "kurdish": "kurdish",
	"tg": "tajik", "tajik": "tajik",
	"ky": "kyrgyz", "kyrgyz": "kyrgyz",
	"mn": "mongolian", "mongolian": "mongolian",
	"bo": "tibetan", "tibetan": "tibetan",
	"dz": "dzongkha", "dzongkha": "dzongkha",
	"ug": "uighur", "uighur": "uighur",
}

// extensionTokens maps a lowercased extension to its EAPI wire token.
var extensionTokens = map[string]string{
	"azw": "AZW", "azw3": "AZW3", "cbz": "CBZ", "djv": "DJV", "djvu": "DJVU", "epub": "EPUB",
	"fb2": "FB2", "lit": "LIT", "mobi": "MOBI", "pdf": "PDF", "rtf": "RTF", "txt": "TXT",
}

// NormaliseLanguages maps ISO 639-1 codes and EAPI names to EAPI names,
// trimming, lowercasing and deduplicating while preserving first-seen order.
func NormaliseLanguages(raw []string) ([]string, error) {
	return normalise(raw, languageNames, "language")
}

// NormaliseFormats maps extensions to uppercase EAPI tokens, trimming,
// lowercasing and deduplicating while preserving first-seen order.
func NormaliseFormats(raw []string) ([]string, error) {
	return normalise(raw, extensionTokens, "format")
}

func normalise(raw []string, table map[string]string, kind string) ([]string, error) {
	out := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, value := range raw {
		mapped, ok := table[strings.ToLower(strings.TrimSpace(value))]
		if !ok {
			return nil, fmt.Errorf("unknown %s %q", kind, value)
		}
		if !seen[mapped] {
			seen[mapped] = true
			out = append(out, mapped)
		}
	}
	return out, nil
}
