package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

//go:embed web/*
var assets embed.FS
var failures atomic.Int64

func reply(w http.ResponseWriter, data any, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		status, msg := 500, "Falha ao concluir a operação. Consulte o terminal."
		switch {
		case errors.Is(err, invalid):
			status, msg = 400, err.Error()
		case errors.Is(err, sql.ErrNoRows):
			status, msg = 404, "Registro não encontrado"
		case errors.Is(err, conflict):
			status, msg = 409, err.Error()
		default:
			failures.Add(1)
			log.Print(err)
		}
		w.WriteHeader(status)
		data = map[string]string{"error": msg}
	}
	json.NewEncoder(w).Encode(data)
}
func rows(db *sql.DB, query string) ([]map[string]any, error) {
	records, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer records.Close()
	names, err := records.Columns()
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for records.Next() {
		values := make([]any, len(names))
		ptr := make([]any, len(names))
		for i := range values {
			ptr[i] = &values[i]
		}
		if err = records.Scan(ptr...); err != nil {
			return nil, err
		}
		item := map[string]any{}
		for i, name := range names {
			item[name] = values[i]
		}
		items = append(items, item)
	}
	return items, records.Err()
}
func (a *App) dashboard() (map[string]any, error) {
	data := map[string]any{"errors": failures.Load()}
	queries := map[string]string{
		"operations": "SELECT id,client,product,destination,amount,created,state,reason,evidence FROM operations ORDER BY created DESC,id LIMIT 60",
		"events":     "SELECT operation,created,kind,detail FROM events WHERE delivered=true ORDER BY created DESC,id LIMIT 60",
		"accounts": `SELECT id, opening + COALESCE((SELECT SUM(cents) FROM entries WHERE account=clients.id),0) AS balance,
 COALESCE((SELECT SUM(amount) FROM operations WHERE client=clients.id AND state='EM_RECONCILIACAO'),0) AS reserved FROM clients ORDER BY id`,
		"stats": `SELECT COUNT(*) AS total,COUNT(CASE WHEN state='AUTORIZADA' THEN 1 END) AS approved,
 COUNT(CASE WHEN state='EM_ANALISE' THEN 1 END) AS review,COUNT(CASE WHEN state='REJEITADA' THEN 1 END) AS denied FROM operations`,
		"queue":    "SELECT COUNT(*) AS pending FROM events WHERE delivered=false",
		"settings": "SELECT enabled FROM settings WHERE id='siem'",
		"disputes": "SELECT operation,protocol,note FROM disputes ORDER BY protocol",
	}
	for k, q := range queries {
		v, err := rows(a.db, q)
		if err != nil {
			return nil, err
		}
		data[k] = v
	}
	var n int
	err := a.db.QueryRow("SELECT COUNT(*) FROM operations").Scan(&n)
	if err != nil {
		return nil, err
	}
	var us int64
	if n > 0 {
		err = a.db.QueryRow("SELECT elapsed FROM operations ORDER BY elapsed LIMIT 1 OFFSET $1", int(math.Ceil(float64(n)*0.95))-1).Scan(&us)
		if err != nil {
			return nil, err
		}
	}
	data["p95"] = float64(us) / 1000
	return data, nil
}
func (a *App) publish() error {
	_, err := a.db.Exec("UPDATE events SET delivered=true WHERE delivered=false AND (SELECT enabled FROM settings WHERE id='siem')=true")
	return err
}
func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/painel", func(w http.ResponseWriter, r *http.Request) { d, e := a.dashboard(); reply(w, d, e) })
	mux.HandleFunc("POST /transacoes", func(w http.ResponseWriter, r *http.Request) {
		var p Payment
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if dec.Decode(&p) != nil {
			reply(w, nil, invalid)
			return
		}
		v, e := a.submit(p)
		reply(w, v, e)
	})
	mux.HandleFunc("GET /transacoes/{id}", func(w http.ResponseWriter, r *http.Request) {
		v := Result{ID: r.PathValue("id")}
		e := a.db.QueryRow("SELECT state,reason,evidence FROM operations WHERE id=$1", v.ID).Scan(&v.State, &v.Reason, &v.Evidence)
		reply(w, v, e)
	})
	mux.HandleFunc("POST /transacoes/{id}/reconciliar", func(w http.ResponseWriter, r *http.Request) { v, e := a.reconcile(r.PathValue("id")); reply(w, v, e) })
	mux.HandleFunc("POST /transacoes/{id}/contestar", func(w http.ResponseWriter, r *http.Request) {
		var d struct{ Note string }
		if json.NewDecoder(r.Body).Decode(&d) != nil {
			reply(w, nil, invalid)
			return
		}
		v, e := a.dispute(r.PathValue("id"), strings.TrimSpace(d.Note))
		reply(w, map[string]string{"protocol": v}, e)
	})
	mux.HandleFunc("POST /api/siem", func(w http.ResponseWriter, r *http.Request) {
		var d struct{ Enabled *bool }
		if json.NewDecoder(r.Body).Decode(&d) != nil || d.Enabled == nil {
			reply(w, nil, invalid)
			return
		}
		_, e := a.db.Exec("UPDATE settings SET enabled=$1 WHERE id='siem'", *d.Enabled)
		reply(w, d, e)
	})
	mux.HandleFunc("POST /api/politica", func(w http.ResponseWriter, r *http.Request) {
		tx, e := a.db.Begin()
		if e == nil {
			defer tx.Rollback()
			e = event(tx, "painel", "ACESSO_NEGADO", "Perfil de demonstração sem permissão para editar controles")
			if e == nil {
				e = tx.Commit()
			}
		}
		if e != nil {
			reply(w, nil, e)
			return
		}
		http.Error(w, "Alteração negada e auditada", http.StatusForbidden)
	})
	dir, _ := fs.Sub(assets, "web")
	mux.Handle("GET /", http.FileServer(http.FS(dir)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, e := url.Parse(origin)
				if e != nil || u.Host != r.Host {
					http.Error(w, "Origem inválida", 403)
					return
				}
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				http.Error(w, "Use application/json", 415)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 8192)
		}
		mux.ServeHTTP(w, r)
	})
}
func main() {
	driver, dsn := "sqlite", ".data/finslet.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	if pg := os.Getenv("DATABASE_URL"); pg != "" {
		driver, dsn = "pgx", pg
	} else if err := os.MkdirAll(".data", 0700); err != nil {
		log.Fatal(err)
	}
	a, err := openStore(driver, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer a.db.Close()
	go func() {
		for range time.Tick(time.Second) {
			if e := a.publish(); e != nil {
				log.Print(e)
			}
		}
	}()
	addr := env("FINSLET_ADDR", "127.0.0.1:8090")
	log.Printf("Finslet em http://%s", addr)
	server := &http.Server{Addr: addr, Handler: a.routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute}
	log.Fatal(server.ListenAndServe())
}
