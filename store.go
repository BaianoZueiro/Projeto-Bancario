package main

import (
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
	"regexp"
	"time"
)

//go:embed schema.sql
var schema string

type App struct {
	db     *sql.DB
	decide Evaluate
}
type Payment struct {
	ID          string `json:"id"`
	Client      string `json:"client"`
	Product     string `json:"product"`
	Destination string `json:"destination"`
	Amount      int64  `json:"amount"`
	MFA         bool   `json:"mfa"`
	Registered  bool   `json:"registered"`
	New         bool   `json:"new"`
	Region      bool   `json:"region"`
	Gateway     string `json:"gateway"`
	Fault       string `json:"fault"`
}
type Result struct {
	ID, State, Reason, Evidence string
	Replay                      bool
}

var validID = regexp.MustCompile("^[a-zA-Z0-9_-]{1,80}$")
var conflict = errors.New("chave reutilizada com dados diferentes")
var invalid = errors.New("dados inválidos")

func uid() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func openStore(driver, dsn string) (*App, error) {
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &App{db, evaluate}, nil
}
func event(tx *sql.Tx, id, kind, detail string) error {
	_, err := tx.Exec("INSERT INTO events(id,operation,created,kind,detail) VALUES($1,$2,$3,$4,$5)", uid(), id, time.Now().UnixMilli(), kind, detail)
	return err
}
func balance(tx *sql.Tx, client string) (int64, error) {
	var n int64
	err := tx.QueryRow(`SELECT opening + COALESCE((SELECT SUM(cents) FROM entries WHERE account=clients.id),0)
 - COALESCE((SELECT SUM(amount) FROM operations WHERE client=clients.id AND state='EM_RECONCILIACAO'),0)
 FROM clients WHERE id=$1`, client).Scan(&n)
	return n, err
}
func book(tx *sql.Tx, id, client, payee string, amount int64) error {
	_, err := tx.Exec("INSERT INTO entries VALUES ($1,$2,$3),($4,$5,$6)", id, client, -amount, id, payee, amount)
	return err
}
func (a *App) submit(p Payment) (Result, error) {
	start := time.Now()
	if !validID.MatchString(p.ID) || !validID.MatchString(p.Destination) || p.Amount <= 0 || p.Amount > 1000000000 ||
		(p.Product != "pix" && p.Product != "cartao") || (p.Gateway != "sucesso" && p.Gateway != "falha" && p.Gateway != "timeout") ||
		(p.Fault != "" && p.Fault != "opa" && p.Fault != "sql") {
		return Result{}, invalid
	}
	raw, _ := json.Marshal(p)
	tx, err := a.db.Begin()
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	// Serializa decisões da mesma conta também no PostgreSQL.
	if _, err = tx.Exec("UPDATE clients SET active=active WHERE id=$1", p.Client); err != nil {
		return Result{}, err
	}
	r := Result{ID: p.ID}
	var old string
	err = tx.QueryRow("SELECT request,state,reason,evidence FROM operations WHERE id=$1", p.ID).Scan(&old, &r.State, &r.Reason, &r.Evidence)
	if err == nil {
		if old != string(raw) {
			return Result{}, conflict
		}
		r.Replay = true
		return r, nil
	}
	if err != sql.ErrNoRows {
		return r, err
	}
	var active bool
	var cap, typical, total int64
	var count int
	err = tx.QueryRow("SELECT active,cap,typical FROM clients WHERE id=$1", p.Client).Scan(&active, &cap, &typical)
	if err == sql.ErrNoRows {
		return r, invalid
	}
	if err != nil {
		return r, err
	}
	window := 3 * time.Minute
	query := "SELECT COUNT(*),COALESCE(SUM(amount),0) FROM operations WHERE client=$1 AND product=$2 AND created >= $3"
	args := []any{p.Client, p.Product, start.Add(-window).UnixMilli()}
	if p.Product == "pix" {
		query += " AND destination=$4"
		args = append(args, p.Destination)
	} else {
		args[2] = start.Add(-time.Minute).UnixMilli()
	}
	if err = tx.QueryRow(query, args...).Scan(&count, &total); err != nil {
		return r, err
	}
	facts := riskFacts(p, active, cap, typical, count+1, total+p.Amount)
	available, err := balance(tx, p.Client)
	if err != nil {
		return r, err
	}
	versions := map[string]string{"perfil": "cadastro-1"}
	r.State, r.Reason = "AUTORIZADA", "OK"
	for _, stage := range []string{"opa", "zen"} {
		var v Verdict
		if p.Fault == "opa" && stage == "opa" {
			err = errors.New("falha de ensaio")
		} else {
			v, err = a.decide(stage, facts)
		}
		if err != nil {
			r.State, r.Reason = "EM_ANALISE", "MOTOR_"+stage
			versions[stage] = "indisponível"
			break
		}
		versions[stage] = v.Version
		if v.Reason != "OK" {
			r.State = "EM_ANALISE"
			if stage == "opa" {
				r.State = "REJEITADA"
			}
			r.Reason = v.Reason
			break
		}
	}
	if r.State == "AUTORIZADA" {
		switch {
		case available < p.Amount:
			r.State, r.Reason = "REJEITADA", "SALDO"
		case p.Gateway == "falha":
			r.State, r.Reason = "REJEITADA", "GATEWAY_RECUSOU"
		case p.Gateway == "timeout":
			r.State, r.Reason = "EM_RECONCILIACAO", "RESPOSTA_PENDENTE"
		}
	}
	evidence, _ := json.Marshal(map[string]any{"regras": versions, "sinais": facts, "saldo_disponivel_antes": available})
	r.Evidence = string(evidence)
	_, err = tx.Exec("INSERT INTO operations VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)", p.ID, string(raw), p.Client, p.Product, p.Destination, p.Amount, start.UnixMilli(), r.State, r.Reason, r.Evidence, time.Since(start).Microseconds())
	if err != nil {
		return r, err
	}
	if r.State == "EM_RECONCILIACAO" || r.State == "AUTORIZADA" {
		// O recibo pertence ao gateway fictício. O timeout simula perda da resposta.
		if _, err = tx.Exec("INSERT INTO gateway_receipts VALUES($1,true)", p.ID); err != nil {
			return r, err
		}
	}
	if r.State == "AUTORIZADA" {
		if err = book(tx, p.ID, p.Client, p.Product+"/"+p.Destination, p.Amount); err != nil {
			return r, err
		}
	}
	if err = event(tx, p.ID, r.State, r.Reason); err != nil {
		return r, err
	}
	if p.Fault == "sql" {
		_, err = tx.Exec("INSERT INTO operations SELECT * FROM operations WHERE id=$1", p.ID)
		return r, err
	}
	return r, tx.Commit()
}
func (a *App) reconcile(id string) (Result, error) {
	tx, err := a.db.Begin()
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE operations SET state=state WHERE id=$1", id); err != nil {
		return Result{}, err
	}
	var client, payee string
	var amount int64
	r := Result{ID: id}
	err = tx.QueryRow("SELECT client,product || '/' || destination,amount,state,reason,evidence FROM operations WHERE id=$1", id).Scan(&client, &payee, &amount, &r.State, &r.Reason, &r.Evidence)
	if err != nil {
		return r, err
	}
	if r.State != "EM_RECONCILIACAO" {
		r.Replay = true
		return r, nil
	}
	var confirmed bool
	if err = tx.QueryRow("SELECT confirmed FROM gateway_receipts WHERE operation=$1", id).Scan(&confirmed); err != nil {
		return r, err
	}
	if confirmed {
		r.State, r.Reason = "AUTORIZADA", "RECIBO_CONFIRMADO"
		if err = book(tx, id, client, payee, amount); err != nil {
			return r, err
		}
	} else {
		r.State, r.Reason = "REJEITADA", "GATEWAY_RECUSOU"
	}
	if _, err = tx.Exec("UPDATE operations SET state=$1,reason=$2 WHERE id=$3", r.State, r.Reason, id); err != nil {
		return r, err
	}
	if err = event(tx, id, r.State, r.Reason); err != nil {
		return r, err
	}
	return r, tx.Commit()
}
func (a *App) dispute(id, note string) (string, error) {
	if len(note) < 3 || len(note) > 300 {
		return "", invalid
	}
	tx, err := a.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var state string
	if err = tx.QueryRow("SELECT state FROM operations WHERE id=$1", id).Scan(&state); err != nil {
		return "", err
	}
	protocol := "CT-" + uid()[:8]
	res, err := tx.Exec("INSERT INTO disputes VALUES($1,$2,$3) ON CONFLICT DO NOTHING", id, protocol, note)
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if n == 0 {
		err = tx.QueryRow("SELECT protocol FROM disputes WHERE operation=$1", id).Scan(&protocol)
	} else {
		err = event(tx, id, "CONTESTACAO", fmt.Sprintf("Aberta • %s", protocol))
	}
	if err != nil {
		return "", err
	}
	return protocol, tx.Commit()
}
