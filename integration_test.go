package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"

	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"
)

// Executa a carga exclusivamente em um schema temporário do banco de ensaio.
func TestCargaPostgresHTTP(t *testing.T) {
	dsn := os.Getenv("FINSLET_TEST_PG")
	if dsn == "" {
		t.Skip("Configure FINSLET_TEST_PG para o banco de ensaio")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	namespace := "ensaio_" + uid()
	if _, err = admin.Exec("CREATE SCHEMA " + namespace); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec("DROP SCHEMA " + namespace + " CASCADE")
	app, err := openStore("pgx", dsn+"&search_path="+namespace)
	if err != nil {
		t.Fatal(err)
	}
	defer app.db.Close()
	srv := httptest.NewUnstartedServer(app.routes())
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	warm, err := srv.Client().Get(srv.URL + "/api/painel")
	if err != nil {
		t.Fatal(err)
	}
	warm.Body.Close()
	const n = 500
	gate := make(chan struct{})
	latencies := make([]time.Duration, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := usual()
			p.ID = fmt.Sprintf("carga-%03d", i)
			p.Amount = 1
			raw, _ := json.Marshal(p)
			<-gate
			start := time.Now()
			res, e := srv.Client().Post(srv.URL+"/transacoes", "application/json", bytes.NewReader(raw))
			if e != nil {
				t.Error(e)
				return
			}
			defer res.Body.Close()
			var r Result
			e = json.NewDecoder(res.Body).Decode(&r)
			latencies[i] = time.Since(start)
			if e != nil || res.StatusCode != 200 || r.State != "AUTORIZADA" {
				t.Error(res.StatusCode, r, e)
			}
		}()
	}
	close(gate)
	wg.Wait()
	if count(t, app.db, "SELECT COUNT(*) FROM operations") != n || count(t, app.db, "SELECT COUNT(*) FROM entries") != 2*n || count(t, app.db, "SELECT SUM(cents) FROM entries") != 0 {
		t.Fatal("ledger inconsistente")
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Logf("%d pedidos simultâneos, p95 HTTP %s", n, latencies[474])
}
func TestRetomada(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "retomada.db")
	first, e := openStore("sqlite", dsn)
	if e != nil {
		t.Fatal(e)
	}
	first.decide = func(string, Facts) (Verdict, error) { return Verdict{"OK", "teste"}, nil }
	p := usual()
	p.Gateway = "timeout"
	_, e = first.submit(p)
	first.db.Close()
	if e != nil {
		t.Fatal(e)
	}
	second, e := openStore("sqlite", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer second.db.Close()
	r, e := second.reconcile(p.ID)
	if e != nil || r.Reason != "RECIBO_CONFIRMADO" {
		t.Fatal(r, e)
	}
}
func TestErrosDeClassificacao(t *testing.T) {
	if os.Getenv("FINSLET_INTEGRACAO") != "1" {
		t.Skip("Requer motores reais")
	}
	cases := []struct {
		fraud, newDestination bool
		amount                int64
	}{
		{false, false, 15000}, {false, true, 600000}, {true, true, 600000}, {true, false, 15000},
	}
	fp, fn := 0, 0
	for _, c := range cases {
		a := testApp(t)
		a.decide = evaluate
		p := usual()
		p.Amount = c.amount
		p.New = c.newDestination
		r, e := a.submit(p)
		if e != nil {
			t.Fatal(e)
		}
		flag := r.State == "EM_ANALISE"
		if flag && !c.fraud {
			fp++
		}
		if !flag && c.fraud {
			fn++
		}
	}
	if fp != 1 || fn != 1 {
		t.Fatal(fp, fn)
	}
	t.Log("Amostra didática: 1 falso positivo em 2 legítimas e 1 falso negativo em 2 fraudes rotuladas.")
}
