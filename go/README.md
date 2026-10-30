# OpCore Simplify — port em Go

Reimplementação progressiva do [OpCore Simplify](../README.md) em Go, usando o código Python original como referência funcional. O objetivo é uma CLI nativa e multiplataforma (Linux, Windows e macOS), com a interface gráfica (Wails) planejada para depois.

Licença: BSD 3-Clause, Copyright (c) 2024, lzhoang2601. Veja [`LICENSE`](LICENSE), que é a cópia do arquivo da raiz do projeto original.

## Estado atual

| Módulo Python original | Pacote Go | Status |
|---|---|---|
| `Scripts/datasets/os_data.py` (catálogo do macOS) | `internal/macos` | Implementado e testado contra o Python |
| `Scripts/utils.py` (`parse_darwin_version`) | `internal/macos` | Implementado e testado contra o Python |
| `Scripts/datasets/mac_model_data.py` + `smbios.select_smbios_model` | — | Pendente |
| `Scripts/compatibility_checker.py` + datasets de CPU/GPU/chipset/PCI | — | Pendente |
| Detecção de hardware (`Report.json` / Hardware Sniffer) | — | Pendente |
| `Scripts/acpi_guru.py`, `dsdt.py`, `datasets/acpi_patch_data.py` | — | Pendente |
| `Scripts/kext_maestro.py`, `datasets/kext_data.py` | — | Pendente |
| `Scripts/smbios.py` (geração de serial/MLB/UUID) | — | Pendente |
| Gerenciamento de plist / geração de EFI (`config_prodigy.py`, `gathering_files.py`) | — | Pendente |
| GUI (Wails) | — | Não iniciado |

Não há pacotes vazios nem placeholders: um pacote só entra no repositório quando tem implementação e testes.

## Módulo implementado: `internal/macos`

- `ParseVersion(s)`: equivalente a `Utils.parse_darwin_version`. Aceita exatamente três componentes inteiros e lê cada um com as mesmas regras do `int()` do Python (espaços nas bordas, sinal, `_` entre dígitos).
- `Version.Compare`: ordena versões como a comparação de tuplas do Python.
- `Releases()`: catálogo do macOS, do High Sierra (17) ao Tahoe (25).
- `LatestDarwinVersion(includeBeta)` e `LowestDarwinVersion()`: equivalentes a `get_latest_darwin_version` e `get_lowest_darwin_version`.
- `MacOSName(darwin)`: equivalente a `get_macos_name_by_darwin`. Como no original, usa só os dois primeiros caracteres como major do Darwin. Retorna `ErrUnknownRelease` quando o Python retornaria `None`.

### Como a equivalência é verificada

`internal/macos/parity_test.go` executa o **código Python original**, sem alterações, através de `testdata/pyref_macos.py` (`python3 -I`). Ele envia as mesmas entradas para os dois lados e compara:

- o catálogo completo (nome, versão, Darwin e status);
- `latest` com e sem beta, e `lowest`;
- entradas válidas e malformadas do parse, incluindo casos em que o Python lança `ValueError` (o Go também precisa falhar);
- nomes para versões conhecidas, versões sem release e prefixos inválidos.

Se `python3` não existir, o teste de paridade é pulado de forma explícita. Os demais testes continuam rodando.

Também foi feita uma verificação de sensibilidade: alterando de propósito o mapeamento do Tahoe e a lógica de `latest`, os testes de catálogo, `latest`, `name` e paridade falharam, e a restauração voltou a passar.

## CLI

```bash
go run ./cmd/opcore-simplify macos latest           # 25.99.99 (inclui beta)
go run ./cmd/opcore-simplify macos latest --stable  # somente releases finais
go run ./cmd/opcore-simplify macos lowest           # 17.0.0
go run ./cmd/opcore-simplify macos name 25.0.0      # macOS Tahoe 26
go run ./cmd/opcore-simplify macos parse +2_5.0.0   # 25.0.0
```

## Verificação

Executada a partir de `go/`:

```bash
gofmt -l .
go vet ./...
go test ./...
go build ./...
GOOS=windows GOARCH=amd64 go build -o /dev/null ./cmd/opcore-simplify
GOOS=darwin  GOARCH=arm64 go build -o /dev/null ./cmd/opcore-simplify
```

Requer Go 1.24 ou superior e, para o teste de paridade, `python3`.

## Diferenças conhecidas em relação ao Python

1. **Inteiros fora do intervalo de `int`**: o Python aceita precisão arbitrária (`"99999999999999999999.0.0"` é válido); o Go retorna erro. Esse caso foi excluído da lista de paridade de propósito.
2. **Dígitos Unicode não ASCII**: o `int()` do Python aceita, por exemplo, `٢٥`; o Go aceita apenas `0-9`.
3. **Catálogo vazio**: o Python retorna `None` em `get_latest_darwin_version`; o Go retorna `""`. Isso não pode acontecer com o catálogo atual.
4. **Sufixo " (Beta)"**: o Go implementa o caminho, mas nenhuma entrada beta existe hoje no catálogo, então esse trecho não é exercitado pelo teste de paridade.
5. **Escopo**: só o módulo `macos` é portado. O menu interativo, a atualização automática, o download e a interface do original ainda não existem em Go.

## Próxima etapa sugerida

Portar `mac_model_data.py` (tabela `mac_devices`) e a função pura `select_smbios_model` de `smbios.py`, sem a parte de geração de serial, que depende de subprocesso (`macserial`) e de download. A paridade seguirá o mesmo padrão: o Python original é o oráculo, com entradas de `Report.json` sintéticas, mas documentadas, e os resultados são comparados caso a caso.

## Créditos

- [OpCore Simplify](https://github.com/lzhoang2801/OpCore-Simplify) (lzhoang2801), BSD 3-Clause.
- [OpenCorePkg](https://github.com/acidanthera/OpenCorePkg) (Acidanthera) e a [Dortania Guide](https://dortania.github.io/OpenCore-Install-Guide/), usados como referência pelo projeto original.
