package ledger_test

import (
	"strings"
	"testing"

	"buscalogo-agent/internal/ledger"
)

func TestValidDomainBLAndLO(t *testing.T) {
	ok := []string{"a.bl", "receitas.bl", "loja.lo", "x1.lo", "ab-c.bl", "Loja.LO"}
	for _, d := range ok {
		if !ledger.ValidDomain(d) {
			t.Fatalf("esperava válido: %q", d)
		}
	}
	bad := []string{"", ".bl", "bl", "foo.com", "foo.xyz", "a.b.bl", "-x.bl", "x-.lo"}
	for _, d := range bad {
		if ledger.ValidDomain(d) {
			t.Fatalf("esperava inválido: %q", d)
		}
	}
}

func TestHasAllowedTLD(t *testing.T) {
	if !ledger.HasAllowedTLD("site.bl") || !ledger.HasAllowedTLD("SITE.LO") {
		t.Fatal("HasAllowedTLD falhou para .bl/.lo")
	}
	if ledger.HasAllowedTLD("example.com") || ledger.HasAllowedTLD("") {
		t.Fatal("HasAllowedTLD aceitou host inválido")
	}
}

func TestDomainHint(t *testing.T) {
	h := ledger.DomainHint()
	if !strings.Contains(h, "nome.bl") || !strings.Contains(h, "nome.lo") {
		t.Fatalf("hint=%q", h)
	}
}
