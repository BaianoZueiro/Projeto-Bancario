param([switch]$SemNavegador, [switch]$SomenteMotores)
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
if (!$SomenteMotores) {
    try {
        $atual=Invoke-RestMethod 'http://127.0.0.1:8090/api/painel' -TimeoutSec 1
        if ($null -ne $atual.accounts -and $null -ne $atual.operations) {
            Write-Host 'Finslet ja esta em http://127.0.0.1:8090'
            if (!$SemNavegador) { Start-Process 'http://127.0.0.1:8090' }
            return
        }
    } catch { }
}
$processos = @()
function Iniciar-ProcessoLocal($arquivo, $argumentos) {
    if ($argumentos) { Start-Process -FilePath $arquivo -ArgumentList $argumentos -WorkingDirectory $PSScriptRoot -WindowStyle Hidden -PassThru } else { Start-Process -FilePath $arquivo -WorkingDirectory $PSScriptRoot -WindowStyle Hidden -PassThru }
}
try {
    $opa = if (Test-Path '.sdk/opa.exe') { (Resolve-Path '.sdk/opa.exe').Path } else { (Get-Command opa).Source }
    $python = $env:FINSLET_PYTHON
    if (!$python -and (Test-Path '.sdk/python-path.txt')) { $python=(Get-Content '.sdk/python-path.txt' -Raw).Trim() }
    if (!$python) {
        $runtime = Get-ChildItem 'C:/Program Files/WindowsApps/OpenAI.CodexPrimaryRuntime*/dependencies/python/python.exe' -ErrorAction SilentlyContinue | Select-Object -Last 1
        $python = if ($runtime) { $runtime.FullName } else { (Get-Command python).Source }
    }
    if (Test-Path '.sdk/zen') { $env:PYTHONPATH = (Resolve-Path '.sdk/zen').Path }
    $processos += Iniciar-ProcessoLocal $opa @('run','--server','--addr=127.0.0.1:8181','policy/controls.rego')
    $processos += Iniciar-ProcessoLocal $python @('policy/service.py')
    for ($i=0; $i -lt 50; $i++) {
        try {
            Invoke-RestMethod 'http://127.0.0.1:8181/health' -TimeoutSec 2 | Out-Null
            $teste = Invoke-RestMethod 'http://127.0.0.1:8182/evaluate' -Method Post -ContentType application/json -Body '{"cardFrequency":false,"pixSequence":false,"atypicalNew":false,"regionNew":false}' -TimeoutSec 2
            if ($teste.reason -eq 'OK') { break }
        } catch { Start-Sleep -Milliseconds 200 }
    }
    if ($i -eq 50 -or ($processos | Where-Object HasExited)) { throw 'Falha ao iniciar OPA ou ZEN. Verifique as dependencias e as portas 8181 e 8182.' }
    if (!$SomenteMotores) {
        $go = if (Test-Path '.sdk/go/bin/go.exe') { (Resolve-Path '.sdk/go/bin/go.exe').Path } else { (Get-Command go).Source }
        if (Test-Path '.sdk/gopath') { $env:GOPATH="$PSScriptRoot/.sdk/gopath"; $env:GOCACHE="$PSScriptRoot/.sdk/cache" }
        & $go build -o bin/finslet.exe .
        if ($LASTEXITCODE -ne 0) { throw 'Falha na compilacao.' }
        $processos += Iniciar-ProcessoLocal (Resolve-Path 'bin/finslet.exe').Path @()
        for ($i=0; $i -lt 40; $i++) {
            try { Invoke-RestMethod 'http://127.0.0.1:8090/api/painel' -TimeoutSec 2 | Out-Null; break } catch { Start-Sleep -Milliseconds 250 }
        }
        if ($i -eq 40 -or $processos[-1].HasExited) { throw 'Falha ao iniciar a API na porta 8090.' }
        Write-Host 'Finslet em http://127.0.0.1:8090'
        if (!$SemNavegador) { Start-Process 'http://127.0.0.1:8090' }
    }
    Write-Host 'Use Ctrl+C para encerrar.'
    while ($true) {
        Start-Sleep -Seconds 2
        if ($processos | Where-Object HasExited) { throw 'Um dos processos foi encerrado.' }
    }
} finally {
    $processos | Where-Object { ! $_.HasExited } | ForEach-Object { Stop-Process -Id $_.Id }
}


