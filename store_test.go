package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func testApp(t *testing.T) *App {
	t.Helper()
	a, e := openStore("sqlite", filepath.Join(t.TempDir(), "teste.db")+"?_pragma=foreign_keys(1)")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.db.Close() })
	a.decide = func(stage string, f Facts) (Verdict, error) { return Verdict{"OK", "teste"}, nil }
	return a
}
func usual() Payment {
	return Payment{ID: uid(), Client: "Bruno", Product: "pix", Destination: "loja", Amount: 15000, MFA: true, Registered: true, Gateway: "sucesso"}
}
func count(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestAtomicidade(t *testing.T) {
	a := testApp(t)
	p := usual()
	p.Fault = "sql"
	if _, e := a.submit(p); e == nil {
		t.Fatal("faltou erro SQL")
	}
	for _, table := range []string{"operations", "entries", "events", "gateway_receipts"} {
		if count(t, a.db, "SELECT COUNT(*) FROM "+table) != 0 {
			t.Fatal(table)
		}
	}
}
func TestReconciliaUmaVez(t *testing.T) {
	a := testApp(t)
	p := usual()
	p.Gateway = "timeout"
	r, e := a.submit(p)
	if e != nil || r.State != "EM_RECONCILIACAO" {
		t.Fatal(r, e)
	}
	tx, _ := a.db.Begin()
	v, e := balance(tx, p.Client)
	tx.Rollback()
	if e != nil || v != 2000000-p.Amount {
		t.Fatal(v, e)
	}
	for range 2 {
		r, e = a.reconcile(p.ID)
		if e != nil || r.State != "AUTORIZADA" {
			t.Fatal(r, e)
		}
	}
	r, e = a.submit(p)
	if e != nil || !r.Replay || count(t, a.db, "SELECT COUNT(*) FROM entries") != 2 {
		t.Fatal(r, e)
	}
	p.Amount++
	if _, e = a.submit(p); !errors.Is(e, conflict) {
		t.Fatal(e)
	}
}
func TestOutboxContestacao(t *testing.T) {
	a := testApp(t)
	p := usual()
	if _, e := a.submit(p); e != nil {
		t.Fatal(e)
	}
	one, e := a.dispute(p.ID, "Compra legítima")
	if e != nil {
		t.Fatal(e)
	}
	two, e := a.dispute(p.ID, "Compra legítima")
	if e != nil || two != one {
		t.Fatal(two, e)
	}
	for _, enabled := range []bool{false, true} {
		if _, e = a.db.Exec("UPDATE settings SET enabled=$1", enabled); e != nil {
			t.Fatal(e)
		}
		if e = a.publish(); e != nil {
			t.Fatal(e)
		}
		n := count(t, a.db, "SELECT COUNT(*) FROM events WHERE delivered=true")
		if (!enabled && n != 0) || (enabled && n != 2) {
			t.Fatal(n)
		}
	}
}
func TestMilReenvios(t *testing.T) {
	a := testApp(t)
	p := usual()
	var wg sync.WaitGroup
	for range 500 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 2 {
				if _, e := a.submit(p); e != nil {
					t.Error(e)
				}
			}
		}()
	}
	wg.Wait()
	if count(t, a.db, "SELECT COUNT(*) FROM entries") != 2 || count(t, a.db, "SELECT COUNT(*) FROM operations") != 1 {
		t.Fatal("efeito duplicado")
	}
}
func TestMotoresReais(t *testing.T) {
	if os.Getenv("FINSLET_INTEGRACAO") != "1" {
		t.Skip("Ative FINSLET_INTEGRACAO com OPA e ZEN em execução")
	}
	cases := []struct {
		name   string
		edit   func(*Payment)
		reason string
	}{
		{"normal", func(p *Payment) {}, "OK"},
		{"conta", func(p *Payment) { p.Client = "Davi" }, "CONTA"},
		{"teto", func(p *Payment) { p.Amount = 120000; p.Client = "Ana" }, "TETO_PESSOAL"},
		{"precedencia", func(p *Payment) { p.Amount = 120000; p.Client = "Ana"; p.Registered = false }, "DISPOSITIVO"},
		{"mfa", func(p *Payment) { p.MFA = false }, "MFA"},
		{"coacao atipica", func(p *Payment) { p.Client = "Carla"; p.Amount = 600000; p.New = true }, "VALOR_ATIPICO"},
		{"regiao isolada", func(p *Payment) { p.Region = true }, "OK"},
		{"regiao combinada", func(p *Payment) { p.Region = true; p.New = true }, "REGIAO_E_DESTINO"},
		{"queda opa", func(p *Payment) { p.Fault = "opa" }, "MOTOR_opa"},
		{"recusa externa", func(p *Payment) { p.Gateway = "falha" }, "GATEWAY_RECUSOU"},
		{"sem saldo", func(p *Payment) { p.Amount = 9999999 }, "SALDO"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := testApp(t)
			a.decide = evaluate
			p := usual()
			c.edit(&p)
			r, e := a.submit(p)
			if e != nil || r.Reason != c.reason {
				t.Fatal(r, e)
			}
		})
	}
	for _, product := range []string{"pix", "cartao"} {
		t.Run(product, func(t *testing.T) {
			a := testApp(t)
			a.decide = evaluate
			n, reason := 3, "PIX_FRACIONADO"
			if product == "cartao" {
				n, reason = 11, "CARTAO_FREQUENTE"
			}
			for i := 1; i <= n; i++ {
				p := usual()
				p.Product = product
				p.New = true
				r, e := a.submit(p)
				exp := "OK"
				if i == n {
					exp = reason
				}
				if e != nil || r.Reason != exp {
					t.Fatal(i, r, e)
				}
			}
		})
	}
}
func TestRecusaContextoInvalido(t *testing.T) {
	a := testApp(t)
	p := usual()
	p.Amount = -1
	if _, e := a.submit(p); !errors.Is(e, invalid) {
		t.Fatal(e)
	}
	req := httptest.NewRequest("POST", "/api/politica", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, req)
	if w.Code != 403 || count(t, a.db, "SELECT COUNT(*) FROM events WHERE kind='ACESSO_NEGADO'") != 1 {
		t.Fatal(fmt.Sprint(w.Code))
	}
}
