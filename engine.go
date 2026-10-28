package main

import (
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind prefixes used in codes
const (
	KName  = "ХҮН"
	KReg   = "РД"
	KPhone = "УТАС"
	KEmail = "ИМЭЙЛ"
)

type Item struct {
	Code string `json:"code"`
	Orig string `json:"orig"`
	Kind string `json:"kind"`
	Num  bool   `json:"num,omitempty"` // original Excel cell was a number
}

// Key is the secret mapping. It never leaves the user's computer.
type Key struct {
	Items    []Item         `json:"items"`
	Names    []string       `json:"names"` // dictionary of known names
	Counters map[string]int `json:"counters"`

	byOrig  map[string]string
	byCode  map[string]*Item
	nameRe  *regexp.Regexp
	Unknown []string `json:"-"`
}

type Stats map[string]int

func NewKey() *Key {
	k := &Key{Counters: map[string]int{}}
	k.index()
	return k
}

func (k *Key) index() {
	if k.Counters == nil {
		k.Counters = map[string]int{}
	}
	k.byOrig = map[string]string{}
	k.byCode = map[string]*Item{}
	for i := range k.Items {
		it := &k.Items[i]
		k.byOrig[it.Kind+"\x00"+it.Orig] = it.Code
		k.byCode[it.Code] = it
	}
}

func (k *Key) codeFor(kind, orig string, num bool) string {
	if c, ok := k.byOrig[kind+"\x00"+orig]; ok {
		return c
	}
	k.Counters[kind]++
	code := fmt.Sprintf("%s_%04d", kind, k.Counters[kind])
	k.Items = append(k.Items, Item{Code: code, Orig: orig, Kind: kind, Num: num})
	k.index()
	return code
}

func (k *Key) AddName(n string) {
	n = strings.TrimSpace(n)
	if !plausibleName(n) {
		return
	}
	for _, x := range k.Names {
		if x == n {
			return
		}
	}
	k.Names = append(k.Names, n)
	// "Бат-Эрдэнэ" is often typed "Бат-эрдэнэ"
	if i := strings.Index(n, "-"); i > 0 && i+1 < len(n) {
		r, sz := utf8.DecodeRuneInString(n[i+1:])
		if unicode.IsUpper(r) {
			k.Names = append(k.Names, n[:i+1]+string(unicode.ToLower(r))+n[i+1+sz:])
		}
	}
	k.nameRe = nil
}

func plausibleName(n string) bool {
	if utf8.RuneCountInString(n) < 2 || utf8.RuneCountInString(n) > 40 {
		return false
	}
	r, _ := utf8.DecodeRuneInString(n)
	if !unicode.IsUpper(r) {
		return false
	}
	for _, c := range n {
		if unicode.IsDigit(c) {
			return false
		}
	}
	return true
}

var weekdays = map[string]bool{"Даваа": true, "Мягмар": true, "Лхагва": true, "Пүрэв": true, "Баасан": true, "Бямба": true, "Ням": true}

const cyr = "А-Яа-яЁёӨөҮү"

var (
	// "Б.Бат-Эрдэнэ", "Ба.Бат-Эрдэнэ", "Бат.Бат-эрдэнэ" (1-3 letters of the father's name)
	reInitialName = regexp.MustCompile(`[А-ЯЁӨҮ][а-яёөүъь]{0,2}\.\s?[А-ЯЁӨҮ][а-яёөүъь]+(?:-[А-ЯЁӨҮа-яёөү][а-яёөүъь]+)?`)
	reEmail       = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	reRegister    = regexp.MustCompile(`[А-ЯЁӨҮ]{2}\d{8}`)
	rePhone       = regexp.MustCompile(`(^|[^0-9_])([789]\d{7})([^0-9]|$)`)
	// tolerant: AI may change case, separator, or use Latin look-alike letters
	reCode = regexp.MustCompile(`(?i)([ХX][ҮYУ][НH]|[РP]Д|[УY]TAC|[УY]ТАС|И-?МЭЙЛ|EMAIL)[_ \-]?(\d{4})`)
	// a case ending the AI attached with a hyphen: ХҮН_0001-ийн
	reHyphenSuffix = regexp.MustCompile(`^-(ын|ийн|ий|ы|тай|тэй|той|төй|ыг|ийг|аас|ээс|оос|өөс|аар|ээр|оор|өөр|д|т|руу|рүү|гаас|гээс|гоос|гөөс|гаар|гээр|гоор|гөөр|гийн|г)(?:[^а-яёөү]|$)`)
)

// nameRe is built lazily from Names (longest first)
func (k *Key) nameRegex() *regexp.Regexp {
	if k.nameRe != nil || len(k.Names) == 0 {
		return k.nameRe
	}
	ns := append([]string(nil), k.Names...)
	sort.Slice(ns, func(i, j int) bool {
		return utf8.RuneCountInString(ns[i]) > utf8.RuneCountInString(ns[j])
	})
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = regexp.QuoteMeta(n)
	}
	k.nameRe = regexp.MustCompile(`(^|[^` + cyr + `A-Za-z_])(` + strings.Join(parts, "|") + `)`)
	return k.nameRe
}

// Anonymize one piece of text. exactKind != "" means the whole value comes
// from a known name/phone/register column.
func (k *Key) Anonymize(s, exactKind string, st Stats) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return s
	}
	if exactKind != "" {
		st[exactKind]++
		return strings.Replace(s, t, k.codeFor(exactKind, t, false), 1)
	}
	s = reEmail.ReplaceAllStringFunc(s, func(m string) string {
		st[KEmail]++
		return k.codeFor(KEmail, m, false)
	})
	s = reInitialName.ReplaceAllStringFunc(s, func(m string) string {
		tail := ""
		if i := strings.LastIndex(m, "-"); i > 0 && caseEndings[m[i+1:]] {
			m, tail = m[:i], m[i:] // "Б.Сараа-тай": keep the case ending
		} else if base, end := splitAttached(m); end != "" {
			m, tail = base, end // "Ш.Төмөртуяад", "Э.Энхмандахтай": ending glued on
		}
		st[KName]++
		return k.codeFor(KName, m, false) + tail
	})
	s = reRegister.ReplaceAllStringFunc(s, func(m string) string {
		st[KReg]++
		return k.codeFor(KReg, m, false)
	})
	s = rePhone.ReplaceAllStringFunc(s, func(m string) string {
		sm := rePhone.FindStringSubmatch(m)
		st[KPhone]++
		return sm[1] + k.codeFor(KPhone, sm[2], false) + sm[3]
	})
	// rePhone consumes the char after a match; run twice for adjacent numbers
	s = rePhone.ReplaceAllStringFunc(s, func(m string) string {
		sm := rePhone.FindStringSubmatch(m)
		st[KPhone]++
		return sm[1] + k.codeFor(KPhone, sm[2], false) + sm[3]
	})
	if re := k.nameRegex(); re != nil {
		var out strings.Builder
		prev := 0
		for _, l := range re.FindAllStringSubmatchIndex(s, -1) {
			name := s[l[4]:l[5]]
			rest := strings.TrimLeft(s[l[5]:], " ")
			if weekdays[name] && strings.HasPrefix(rest, "гараг") {
				continue // "Даваа гараг" is a weekday, not a person
			}
			out.WriteString(s[prev:l[4]])
			out.WriteString(k.codeFor(KName, name, false))
			st[KName]++
			prev = l[5]
		}
		out.WriteString(s[prev:])
		s = out.String()
	}
	return s
}

var caseEndings = map[string]bool{}

func init() {
	for _, e := range strings.Split("ын ийн ий ы тай тэй той төй ыг ийг г аас ээс оос өөс аар ээр оор өөр д т руу рүү гаас гээс гийн нар", " ") {
		caseEndings[e] = true
	}
}

func codeKind(p string) string {
	r, _ := utf8.DecodeRuneInString(strings.ToUpper(p))
	switch r {
	case 'Х', 'X':
		return KName
	case 'Р', 'P':
		return KReg
	case 'И', 'E':
		return KEmail
	}
	return KPhone
}

// vowel class of the last harmony-relevant vowel: 'a' (а,у,я), 'o' (о,ё), 'e' (э,ү,е,и), 'ө' (ө)
func vowelClass(s string) rune {
	rs := []rune(strings.ToLower(s))
	for i := len(rs) - 1; i >= 0; i-- {
		switch rs[i] {
		case 'а', 'у', 'я', 'ы':
			return 'a'
		case 'о', 'ё':
			return 'o'
		case 'ө':
			return 'ө'
		case 'э', 'ү', 'е':
			return 'e'
		}
	}
	return 'e'
}

// fixEnding re-chooses a hyphenated case ending so it agrees with the real name.
func fixEnding(name, end string) string {
	vc := vowelClass(name)
	back := vc == 'a' || vc == 'o'
	rs := []rune(strings.ToLower(name))
	endsVowel := false // long vowel or diphthong at the end: Сараа, Туяа, Цэрэндолгор-ой
	if n := len(rs); n >= 2 {
		last2 := string(rs[n-2:])
		for _, v := range []string{"аа", "ээ", "оо", "өө", "уу", "үү", "яа", "иа", "ай", "эй", "ой", "уй", "үй", "ий", "ёо", "еэ"} {
			if last2 == v {
				endsVowel = true
			}
		}
	}
	if endsVowel {
		// Сараа → Сараагийн, Сараатай, Сараагаас, Сараагаар, Сарааг
		switch end {
		case "ын", "ийн", "гийн":
			return "гийн"
		case "ыг", "ийг", "г":
			return "г"
		case "аас", "ээс", "оос", "өөс", "гаас", "гээс", "гоос", "гөөс":
			end = "гаас"
		case "аар", "ээр", "оор", "өөр":
			end = "гаар"
		}
	}
	pick := func(a, o, e, oe string) string {
		switch vc {
		case 'a':
			return a
		case 'o':
			return o
		case 'ө':
			return oe
		}
		return e
	}
	switch end {
	case "ын", "ийн", "гийн":
		if back {
			return "ын"
		}
		return "ийн"
	case "ыг", "ийг":
		if back {
			return "ыг"
		}
		return "ийг"
	case "тай", "тэй", "той", "төй":
		return pick("тай", "той", "тэй", "төй")
	case "аас", "ээс", "оос", "өөс":
		return pick("аас", "оос", "ээс", "өөс")
	case "аар", "ээр", "оор", "өөр":
		return pick("аар", "оор", "ээр", "өөр")
	case "гаар", "гээр", "гоор", "гөөр":
		return pick("гаар", "гоор", "гээр", "гөөр")
	case "гаас", "гээс", "гоос", "гөөс":
		return pick("гаас", "гоос", "гээс", "гөөс")
	case "руу", "рүү":
		if back {
			return "руу"
		}
		return "рүү"
	}
	return end
}

// attached case endings, longest first. "д" is only split after a long vowel
// so that names such as Болд, Ганболд are never cut.
var attachedEndings = []string{"гийн", "гаас", "гээс", "гоос", "гөөс", "гаар", "гээр", "гоор", "гөөр",
	"тайгаа", "тэйгээ", "тойгоо", "ийн", "тай", "тэй", "той", "төй", "аас", "ээс", "оос", "өөс",
	"аар", "ээр", "оор", "өөр", "ийг", "ын", "ыг", "д", "ад", "эд", "од", "өд"}

// splitAttached splits "Х.Нэртэй" into "Х.Нэр" + "тэй". The result always
// joins back to exactly the input, so restoring stays lossless.
func splitAttached(m string) (string, string) {
	dot := strings.Index(m, ".")
	for _, e := range attachedEndings {
		if !strings.HasSuffix(m, e) {
			continue
		}
		base := m[:len(m)-len(e)]
		name := []rune(strings.TrimSpace(base[dot+1:]))
		if len(name) < 3 {
			continue
		}
		if e == "д" || e == "ад" || e == "эд" || e == "од" || e == "өд" {
			if e == "д" && !longEndRe.MatchString(string(name)) {
				continue
			}
			if e != "д" && longEndRe.MatchString(string(name)) {
				continue
			}
		}
		if e == "ын" || e == "ыг" {
			// Болорсүрэн+ийн style names end in a consonant
			last := name[len(name)-1]
			if strings.ContainsRune("аэиоуөүяеё", last) {
				continue
			}
		}
		return base, e
	}
	return m, ""
}

var longEndRe = regexp.MustCompile(`(аа|ээ|оо|өө|уу|үү|яа|иа|ай|эй|ой|уй|үй|ий)$`)

// Restore replaces codes with the original values. Codes the key does not
// know are left untouched and listed in Unknown.
func (k *Key) Restore(s string, st Stats) string {
	locs := reCode.FindAllStringSubmatchIndex(s, -1)
	var out strings.Builder
	prev := 0
	for _, l := range locs {
		code := codeKind(s[l[2]:l[3]]) + "_" + s[l[4]:l[5]]
		it, ok := k.byCode[code]
		if !ok {
			st["unknown"]++
			k.Unknown = append(k.Unknown, s[l[0]:l[1]])
			continue
		}
		out.WriteString(s[prev:l[0]])
		rebuilt := out.String()
		out.WriteString(it.Orig)
		st[it.Kind]++
		prev = l[1]
		if it.Kind == KName {
			if m := reHyphenSuffix.FindStringSubmatch(s[prev:]); m != nil {
				orig := it.Orig
				if base, end := splitAttached(orig); end != "" {
					orig = base // avoid "Энхмандахтай-тай"
				}
				out.Reset()
				out.WriteString(rebuilt)
				out.WriteString(orig + "-" + fixEnding(orig, m[1]))
				prev += len("-" + m[1])
			}
		}
	}
	out.WriteString(s[prev:])
	return out.String()
}

func xmlUnescape(s string) string { return html.UnescapeString(s) }
func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
