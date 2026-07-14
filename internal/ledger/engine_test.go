package ledger_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"buscalogo-agent/internal/ledger"
	"buscalogo-agent/internal/store"
)

func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func newEng(t *testing.T, engine string) *ledger.Engine {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "registry")
	if engine == "sqlite" {
		path = filepath.Join(dir, "registry.db")
	}
	st, err := store.Open(engine, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return ledger.NewEngine(st, nil)
}

func TestRegisterUpdateTransfer(t *testing.T) {
	for _, engName := range []string{"badger", "sqlite"} {
		t.Run(engName, func(t *testing.T) {
			eng := newEng(t, engName)
			priv := testKey(t)
			ev, err := eng.SignAndApply(priv, ledger.TypeRegister, "receitas.bl", ledger.Records{
				AAAA: []string{"200::1"},
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if ev.Nonce != 0 {
				t.Fatalf("nonce=%d", ev.Nonce)
			}
			rec, err := eng.Lookup("receitas.bl")
			if err != nil || rec == nil {
				t.Fatalf("lookup: %v %#v", err, rec)
			}
			if len(rec.Addresses) != 1 || rec.Addresses[0] != "200::1" {
				t.Fatalf("addrs=%v", rec.Addresses)
			}

			_, err = eng.SignAndApply(priv, ledger.TypeUpdate, "receitas.bl", ledger.Records{
				AAAA: []string{"200::2"},
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			rec, _ = eng.Lookup("receitas.bl")
			if rec.Addresses[0] != "200::2" {
				t.Fatalf("after update %v", rec.Addresses)
			}

			// Replay nonce → fail
			bad := &ledger.DomainEvent{
				Type:        ledger.TypeUpdate,
				Domain:      "receitas.bl",
				Records:     ledger.Records{AAAA: []string{"200::9"}},
				Nonce:       1,
				Timestamp:   ev.Timestamp + 1000,
			}
			if err := bad.Sign(priv); err != nil {
				t.Fatal(err)
			}
			if _, err := eng.Apply(bad); err == nil {
				t.Fatal("expected replay reject")
			}

			other := testKey(t)
			pub := other.Public().(ed25519.PublicKey)
			_, err = eng.SignAndApply(priv, ledger.TypeTransfer, "receitas.bl", ledger.Records{}, pub)
			if err != nil {
				t.Fatal(err)
			}
			// Ex-dono não pode UPDATE
			_, err = eng.SignAndApply(priv, ledger.TypeUpdate, "receitas.bl", ledger.Records{AAAA: []string{"200::3"}}, nil)
			if err == nil {
				t.Fatal("ex-owner update should fail")
			}
			_, err = eng.SignAndApply(other, ledger.TypeUpdate, "receitas.bl", ledger.Records{AAAA: []string{"200::3"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFirstValidWinsOrderIndependent(t *testing.T) {
	privA := testKey(t)
	privB := testKey(t)
	now := time.Now().UnixMilli()

	makeReg := func(priv ed25519.PrivateKey, ts int64, name string) *ledger.DomainEvent {
		ev := &ledger.DomainEvent{
			Type:      ledger.TypeRegister,
			Domain:    "dup.bl",
			Records:   ledger.Records{AAAA: []string{name}},
			Nonce:     0,
			Timestamp: ts,
		}
		if err := ev.Sign(priv); err != nil {
			t.Fatal(err)
		}
		return ev
	}

	// A earlier than B → A wins regardless of ingest order
	a := makeReg(privA, now-2000, "200::a")
	b := makeReg(privB, now-1000, "200::b")

	run := func(t *testing.T, first, second *ledger.DomainEvent, wantAAAA string) {
		eng := newEng(t, "badger")
		if _, err := eng.Apply(first); err != nil {
			t.Fatal(err)
		}
		_, _ = eng.Apply(second) // may fail if loser
		rec, err := eng.Lookup("dup.bl")
		if err != nil || rec == nil {
			t.Fatalf("lookup: %v", err)
		}
		if rec.Addresses[0] != wantAAAA {
			t.Fatalf("want %s got %v", wantAAAA, rec.Addresses)
		}
	}

	t.Run("a_then_b", func(t *testing.T) { run(t, a, b, "200::a") })
	t.Run("b_then_a", func(t *testing.T) { run(t, b, a, "200::a") })

	// Tie on timestamp → smaller hash wins
	c1 := makeReg(privA, now, "200::c1")
	c2 := makeReg(privB, now, "200::c2")
	want := "200::c1"
	if bytes.Compare(c1.Hash(), c2.Hash()) > 0 {
		want = "200::c2"
	}
	t.Run("hash_tiebreak", func(t *testing.T) {
		eng := newEng(t, "sqlite")
		if _, err := eng.Apply(c1); err != nil {
			t.Fatal(err)
		}
		_, _ = eng.Apply(c2)
		rec, err := eng.Lookup("dup.bl")
		if err != nil || rec == nil {
			t.Fatalf("lookup: %v", err)
		}
		if rec.Addresses[0] != want {
			t.Fatalf("want %s got %v (h1=%x h2=%x)", want, rec.Addresses, c1.Hash()[:4], c2.Hash()[:4])
		}
	})
}

func TestHistoricalBatchBypassesRateLimit(t *testing.T) {
	eng := newEng(t, "sqlite")
	priv := testKey(t)
	now := time.Now().UnixMilli()
	var raws [][]byte
	for i := 0; i < 8; i++ {
		ev := &ledger.DomainEvent{
			Type:      ledger.TypeRegister,
			Domain:    fmt.Sprintf("d%d.bl", i),
			Records:   ledger.Records{AAAA: []string{fmt.Sprintf("200::%d", i)}},
			Nonce:     0,
			Timestamp: now - int64(i*1000),
		}
		if err := ev.Sign(priv); err != nil {
			t.Fatal(err)
		}
		raw, err := ev.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		raws = append(raws, raw)
	}
	applied, _, failed, errs := eng.IngestHistoricalBatch(raws)
	if failed > 0 {
		t.Fatalf("failed=%d errs=%v", failed, errs)
	}
	if applied < 8 {
		t.Fatalf("expected 8 registers via catch-up, got applied=%d", applied)
	}
	for i := 0; i < 8; i++ {
		rec, err := eng.Lookup(fmt.Sprintf("d%d.bl", i))
		if err != nil || rec == nil {
			t.Fatalf("missing d%d.bl: %v", i, err)
		}
	}
}
