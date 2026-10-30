# OpCore Simplify — port em Go

Reimplementação progressiva do [OpCore Simplify](../README.md) em Go, usando o código Python original como referência funcional. O objetivo é uma CLI nativa e multiplataforma (Linux, Windows e macOS), com a interface gráfica (Wails) planejada para depois.

Licença: BSD 3-Clause, Copyright (c) 2024, lzhoang2601. Veja [`LICENSE`](LICENSE), que é a cópia do arquivo da raiz do projeto original.

## Estado atual

| Módulo Python original | Pacote Go | Status |
|---|---|---|
| `Scripts/datasets/os_data.py` (catálogo do macOS) | `internal/macos` | Implementado e testado contra o Python |
| `Scripts/utils.py` (`parse_darwin_version`) | `internal/macos` | Implementado e testado contra o Python |
| `Scripts/datasets/mac_model_data.py` (tabela `mac_devices`) | `internal/smbios` | Implementado e testado contra o Python (gerado a partir do original) |
| `smbios.SMBIOS.select_smbios_model` | `internal/smbios` | Implementado e testado contra o Python (38.880 casos) |
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

## Módulo implementado: `internal/smbios` (seleção de modelo)

- `devices_gen.go`: tabela `mac_devices` (77 modelos). É gerada por `tools/gen_mac_devices.py` a partir de `Scripts/datasets/mac_model_data.py`, então não é transcrita à mão. `devices.go` define o tipo `Device`, `Devices()` e `DeviceByName()`.
- `select.go`: `SelectDefaultModel(report, macosVersion)`, equivalente a `select_smbios_model`. O `HardwareReport` contém só os campos usados pelo original: placa-mãe (nome e plataforma), CPU (codename, nome do processador e número de núcleos) e a lista ordenada de GPUs.
- Avaliação preguiçosa igual à do Python: a versão do macOS só é parseada, e a GPU só é lida, nos ramos em que o original faria isso. Um erro aparece só onde o Python também falharia. Caso contrário, o `Laptop` sem GPU retornaria um modelo, como no original.

### Como a equivalência é verificada

`internal/smbios/parity_test.go` executa `testdata/pyref_smbios.py`, que chama o `select_smbios_model` original sem alterações. A matriz tem 38.880 casos: 4 combinações de placa, 24 codenames (incluindo um desconhecido), 3 nomes de processador (um deles com `-8`, que muda o ramo do MacBook Pro), 5 conjuntos de GPUs, 3 contagens de núcleos e 9 versões do macOS (incluindo uma malformada). O teste também compara a tabela de dispositivos inteira.

Verificação de sensibilidade: alterando de propósito o `Macmini7,1` do ramo Haswell/NUC, o teste reportou divergências e foi restaurado em seguida.

`tools/gen_mac_devices.py` gera o arquivo com a mesma saída committed. A conferência foi feita com `diff`.

### Diferenças e limitações do módulo

1. `DiscreteGPU` vazio representa `None` do Python.
2. `CoreCount` é `int`. O original faz `int(...)` sobre o valor do relatório. Um valor ausente é tratado como 0, e não gera erro como no Python.
3. A seleção cobre só o modelo padrão. `customize_smbios_model`, `smbios_specific_options` (que decide NoTouchID e RestrictEvents) e a interface interativa não estão portados.
4. Geração de serial, MLB, ROM e UUID (`generate_smbios`) não foi portada. Depende do `macserial` e de download.

## Próxima etapa sugerida

Portar a geração de SMBIOS (`generate_smbios`): `generate_random_mac` e o formato `MLB`/`SystemSerialNumber` com os valores de fallback. O `macserial` é um binário externo. Antes de implementar, é preciso decidir como ele será invocado em cada plataforma e como o fallback será testado sem executá-lo. Depois, as regras de compatibilidade (`compatibility_checker.py`).

## Créditos

- [OpCore Simplify](https://github.com/lzhoang2801/OpCore-Simplify) (lzhoang2801), BSD 3-Clause.
- [OpenCorePkg](https://github.com/acidanthera/OpenCorePkg) (Acidanthera) e a [Dortania Guide](https://dortania.github.io/OpenCore-Install-Guide/), usados como referência pelo projeto original.
