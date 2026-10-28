package main

import (
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
