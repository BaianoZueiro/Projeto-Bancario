package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"
)

type Verdict struct {
	Reason  string `json:"reason"`
	Version string `json:"version"`
}
type Facts struct {
	Active        bool  `json:"active"`
	MFA           bool  `json:"mfa"`
	Registered    bool  `json:"registered"`
	Amount        int64 `json:"amount"`
	Cap           int64 `json:"cap"`
	CardFrequency bool  `json:"cardFrequency"`
	PixSequence   bool  `json:"pixSequence"`
	AtypicalNew   bool  `json:"atypicalNew"`
	RegionNew     bool  `json:"regionNew"`
	Count         int   `json:"count"`
	Total         int64 `json:"total"`
}
type Evaluate func(string, Facts) (Verdict, error)

var httpEngine = &http.Client{Timeout: 1500 * time.Millisecond}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func evaluate(stage string, f Facts) (Verdict, error) {
	url := env("ZEN_URL", "http://127.0.0.1:8182/evaluate")
	var input any = f
	if stage == "opa" {
		url = env("OPA_URL", "http://127.0.0.1:8181/v1/data/finslet/decision")
		input = map[string]any{"input": f}
	}
	body, err := json.Marshal(input)
	if err != nil {
		return Verdict{}, err
	}
	resp, err := httpEngine.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return Verdict{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Verdict{}, errors.New("motor indisponível")
	}
	var v Verdict
	if stage == "opa" {
		var wrap struct{ Result Verdict }
		err = json.NewDecoder(resp.Body).Decode(&wrap)
		v = wrap.Result
	} else {
		err = json.NewDecoder(resp.Body).Decode(&v)
	}
	if err != nil || v.Reason == "" || v.Version == "" {
		return Verdict{}, errors.New("resposta inválida")
	}
	return v, nil
}

// Pureza: as mesmas entradas produzem os mesmos sinais, sem acesso a banco ou rede.
func riskFacts(p Payment, active bool, cap, typical int64, count int, total int64) Facts {
	return Facts{Active: active, MFA: p.MFA, Registered: p.Registered, Amount: p.Amount, Cap: cap, Count: count, Total: total,
		CardFrequency: p.Product == "cartao" && count >= 11, PixSequence: p.Product == "pix" && p.New && count >= 3 && total >= 30000,
		AtypicalNew: p.New && p.Amount >= 10*typical, RegionNew: p.New && p.Region}
}
