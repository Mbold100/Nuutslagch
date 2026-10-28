package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Anonymize → Restore must give back exactly the original text.
func TestRoundTripText(t *testing.T) {
	cases := []string{
		"Ба.Бат-Эрдэнэ болон Бо.Бат-Эрдэнэ нар, Б.Бат-эрдэнэ-тэй, Б.Сараа-тай уулзав.",
		"Эн.Энхмандахтай хамт, Ша.Төмөртуяад өгсөн, С.Хишигтуяагийн ажил, Б.Болд, Г.Ганболд ирсэн",
		"Б.Болд (bold.b85@gmail.com, 99112233, УБ85010112) болон Д.Сараа saraa@example.mn",
	}
	for _, src := range cases {
		k := NewKey()
		a := k.Anonymize(src, "", Stats{})
		for _, leak := range []string{"Бат-Эрдэнэ", "Болд", "Сараа", "99112233", "УБ85010112", "gmail.com"} {
			if strings.Contains(a, leak) {
				t.Errorf("leak %q in %q", leak, a)
			}
		}
		if r := k.Restore(a, Stats{}); r != src {
			t.Errorf("round trip:\n got %q\nwant %q", r, src)
		}
	}
}

// Two people who differ only by a two-letter initial must get different codes.
func TestSameNameDifferentInitial(t *testing.T) {
	k := NewKey()
	a := k.Anonymize("Ба.Бат-Эрдэнэ, Бо.Бат-Эрдэнэ", "", Stats{})
	if a != "ХҮН_0001, ХҮН_0002" {
		t.Fatalf("got %q", a)
	}
}

// AI output may change code spelling and add case endings.
func TestRestoreAIText(t *testing.T) {
	k := NewKey()
	k.Anonymize("Б.Сараа, Д.Болд, Э.Энхмандахтай", "", Stats{})
	st := Stats{}
	got := k.Restore("хүн_0001-ийн тайлан, XYH 0002-тэй, ХҮН_0003-тай, ХҮН_0099", st)
	want := "Б.Сараа-гийн тайлан, Д.Болд-той, Э.Энхмандах-тай, ХҮН_0099"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if st["unknown"] != 1 || len(k.Unknown) != 1 {
		t.Errorf("unknown code not reported: %v %v", st, k.Unknown)
	}
}

// Weekdays are not people.
func TestWeekday(t *testing.T) {
	k := NewKey()
	k.AddName("Даваа")
	a := k.Anonymize("Даваа гарагт Даваа ирнэ", "", Stats{})
	if a != "Даваа гарагт ХҮН_0001 ирнэ" {
		t.Errorf("got %q", a)
	}
}

func TestExampleFiles(t *testing.T) {
	for _, f := range []string{"examples/ajiltnuud_500_zohiomol.xlsx", "examples/tailan_150_zohiomol.docx"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Skip(err)
		}
		k := NewKey()
		anon, name, err := processFile("anon", f, data, k, Stats{})
		if err != nil {
			t.Fatal(err)
		}
		back, _, err := processFile("restore", name, anon, k, Stats{})
		if err != nil {
			t.Fatal(err)
		}
		k2 := NewKey()
		again, _, _ := processFile("anon", f, back, k2, Stats{})
		if len(again) == 0 {
			t.Fatal("restored file is not readable")
		}
	}
}

// Many office sheets start with a title; the header row is lower down.
func TestTitleRowAboveHeader(t *testing.T) {
	data, err := os.ReadFile("testdata/garchigtai.xlsx")
	if err != nil {
		t.Skip(err)
	}
	k := NewKey()
	st := Stats{}
	if _, _, err := processFile("anon", "garchigtai.xlsx", data, k, st); err != nil {
		t.Fatal(err)
	}
	if st[KName] != 4 || st[KPhone] != 2 || st[KReg] != 2 {
		t.Errorf("columns under a title row not detected: %v", st)
	}
}

func TestLocalOnly(t *testing.T) {
	h := localOnly("127.0.0.1:5000", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for _, c := range []struct {
		host, origin string
		want         int
	}{
		{"127.0.0.1:5000", "", 200},
		{"127.0.0.1:5000", "http://127.0.0.1:5000", 200},
		{"evil.example:5000", "", 403},
		{"127.0.0.1:5000", "http://evil.example", 403},
	} {
		r := httptest.NewRequest("POST", "/api/process", nil)
		r.Host = c.host
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%+v: got %d", c, w.Code)
		}
	}
}
