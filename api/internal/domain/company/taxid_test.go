package company

import "testing"

func TestNormalizeTaxIDStripsAMaskAndNamesTheKind(t *testing.T) {
	canonical, kind, ok := NormalizeTaxID("11.222.333/0001-81")
	if !ok || canonical != "11222333000181" || kind != KindCNPJ {
		t.Fatalf("got %q %q %v, want 11222333000181 cnpj true", canonical, kind, ok)
	}
	canonical, kind, ok = NormalizeTaxID("529.982.247-25")
	if !ok || canonical != "52998224725" || kind != KindCPF {
		t.Fatalf("got %q %q %v, want 52998224725 cpf true", canonical, kind, ok)
	}
}

// The alphanumeric CNPJ, using the Receita Federal's own worked example. The
// first twelve positions may be letters; the two check digits may not.
func TestNormalizeTaxIDAcceptsAnAlphanumericCNPJ(t *testing.T) {
	canonical, kind, ok := NormalizeTaxID("12.ABC.345/01DE-35")
	if !ok || canonical != "12ABC34501DE35" || kind != KindCNPJ {
		t.Fatalf("got %q %q %v, want 12ABC34501DE35 cnpj true", canonical, kind, ok)
	}
}

// Lowercase is what a person types. Storing it as typed would let one company
// register twice, once in each case, and the lock row would not notice.
func TestNormalizeTaxIDUppercasesLetters(t *testing.T) {
	canonical, _, ok := NormalizeTaxID("12.abc.345/01de-35")
	if !ok || canonical != "12ABC34501DE35" {
		t.Fatalf("got %q %v, want 12ABC34501DE35 true", canonical, ok)
	}
}

// A transposed character is the typo this catches, and the reason a length
// check alone is not enough: the length survives a transposition.
func TestNormalizeTaxIDRejectsABadCheckDigit(t *testing.T) {
	for _, in := range []string{"11222333000182", "12ABC34501DE36", "52998224726"} {
		if _, _, ok := NormalizeTaxID(in); ok {
			t.Errorf("%q: accepted, want rejected", in)
		}
	}
}

// The check digits are numeric by rule. A letter there would otherwise be fed
// to the modulus arithmetic and could verify by accident.
func TestNormalizeTaxIDRejectsLettersInTheCheckDigits(t *testing.T) {
	if _, _, ok := NormalizeTaxID("12ABC34501DEX5"); ok {
		t.Error("accepted a letter in a check digit")
	}
}

// A CPF is numeric everywhere. Eleven characters with a letter is not a
// short CNPJ, it is nothing.
func TestNormalizeTaxIDRejectsLettersInACPF(t *testing.T) {
	if _, _, ok := NormalizeTaxID("5299822472A"); ok {
		t.Error("accepted a letter in a CPF")
	}
}

// Repeated characters pass the modulus arithmetic and are the classic filler
// value. They must be refused explicitly or 00000000000000 is a valid CNPJ.
func TestNormalizeTaxIDRejectsRepeatedCharacters(t *testing.T) {
	for _, in := range []string{"00000000000000", "11111111111"} {
		if _, _, ok := NormalizeTaxID(in); ok {
			t.Errorf("%q: accepted, want rejected", in)
		}
	}
}

func TestNormalizeTaxIDRejectsTheWrongLength(t *testing.T) {
	for _, in := range []string{"", "123", "112223330001812"} {
		if _, _, ok := NormalizeTaxID(in); ok {
			t.Errorf("%q: accepted, want rejected", in)
		}
	}
}

// Punctuation is a mask; anything else is a character somebody typed by
// mistake and must not be silently dropped into a valid document.
func TestNormalizeTaxIDRejectsStrayCharacters(t *testing.T) {
	for _, in := range []string{"11222333000181 x", "112223330001*1", "11222333000181\n\n0"} {
		if _, _, ok := NormalizeTaxID(in); ok {
			t.Errorf("%q: accepted, want rejected", in)
		}
	}
}
