# Guia de teste da Beta (no seu notebook)

Este guia é para você rodar o port em Go no seu notebook de testes, onde o download do OpenCorePkg, dos kexts e do `macserial` acontece. Nada disso foi executado no ambiente de desenvolvimento.

## 1. Compilar

Com o Go 1.24 ou mais novo, a partir da pasta `go/`:

```bash
go build -o opcore-simplify ./cmd/opcore-simplify
```

Para compilar para outro sistema (no Windows o executável precisa de `.exe`):

```bash
GOOS=windows GOARCH=amd64 go build -o opcore-simplify.exe ./cmd/opcore-simplify
GOOS=darwin  GOARCH=arm64 go build -o opcore-simplify ./cmd/opcore-simplify
GOOS=linux   GOARCH=amd64 go build -o opcore-simplify ./cmd/opcore-simplify
```

## 2. Preparar a pasta de execução

O executável usa a pasta onde está como base (como o script Python usa a pasta do script):

- `Scripts/` ao lado do executável: é onde ficam o `iasl` (e o `macserial`, o histórico de downloads e o cache `OCK_Files`).
- `Results/` ao lado do executável: é onde a EFI é gerada.

Coloque o `iasl` em `Scripts/` (nomes aceitos: `iasl-dev`, `iasl-stable` ou `iasl`; no Windows, `iasl.exe`). O download automático do `iasl` ainda não foi portado: sem ele o programa termina com a mesma mensagem do original ("Could not locate or download iasl!").

## 3. Rodar

```bash
./opcore-simplify
```

Sem argumentos abre o menu interativo, igual ao `OpCore-Simplify.py`. O subcomando `macos` continua funcionando (`./opcore-simplify macos latest`).

## 4. O que comparar com o Python original

Use o mesmo `Report.json`, as mesmas respostas e (se possível) as mesmas tabelas ACPI nos dois, e compare a pasta `Results`:

```bash
# Python (na raiz do repositório) e Go (na pasta do executável)
diff -r Results-python Results-go
```

O `config.plist` tem campos aleatórios (serial, MLB, ROM, UUID e o layout de áudio sorteado), então ignore essas linhas na comparação. O restante (ACPI, Kernel > Add, DeviceProperties, drivers, kexts, ferramentas) deve ser idêntico.

## 5. O que me enviar quando algo diferir

- O trecho de texto da tela onde o comportamento mudou (ou o erro mostrado em "An Error Occurred").
- O `Report.json` (sem dados sensíveis, se houver) e as respostas que você digitou.
- A saída de `diff -r` entre os dois `Results`.

## Observações

- `updater.py` não foi portado (não há auto-atualização).
- Em versões do Python anteriores à 3.13, algumas mensagens de erro de JSON do original são diferentes das do port.
- Os binários `macserial`, `iasl`, `netsh`, `nmcli`, `networksetup` e `security` só foram exercitados com simulações; este é o primeiro teste real deles.
