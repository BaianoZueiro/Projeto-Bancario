package finslet
import rego.v1

# As condições obrigatórias têm precedência sobre as preferências individuais.
default reason := "OK"
reason := "CONTA" if { not input.active }
else := "MFA" if { not input.mfa }
else := "DISPOSITIVO" if {
    not input.registered
    input.amount > 20000
}
else := "TETO_PESSOAL" if {
    input.cap > 0
    input.amount > input.cap
}
decision := {"reason": reason, "version": "controles-1"}
