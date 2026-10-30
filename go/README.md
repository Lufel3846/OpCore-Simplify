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
| `utils.py` (leitura/escrita de plist via `plistlib`, XML) | `internal/plist` | Implementado e testado contra o `plistlib` (binário pendente) |
| `utils.py` (JSON via `json`, busca de caminhos, zip, leitura/escrita de arquivos) | `internal/pyjson`, `internal/fsutil` | Implementado e testado contra o Python |
| `integrity_checker.py` | `internal/integrity` | Implementado e testado contra o Python |
| `resource_fetcher.py` (requisições, gzip/deflate, download com SHA-256) | `internal/fetch` | Implementado e testado contra o Python, com servidor local |
| `github.py` (parsers das páginas de release) | `internal/github` | Parsers implementados e testados contra o Python (`get_commits` não portado) |
| `gathering_files.py` | `internal/gathering` | Implementado e testado contra o Python em 8 cenários de fluxo completo. `process_kext` pendente (ver abaixo) |
| `kext_maestro.py` (seleção, verificação, instalação, `load_kexts`) | `internal/kextmaestro` | Implementado e testado contra o Python. Menu interativo e Wi-Fi via `wifi_profile_extractor` pendentes (ver abaixo) |
| `kext_data.py`, `pci_data.py`, `cpu_data.py`, `codec_layouts.py` | `internal/datasets` | Gerados a partir do Python por `tools/gen_datasets.py` (dicionários do `pci_data` ainda não gerados) |
| Semântica de Python usada pelos módulos (`dict.get`, `in`, `len`, `int()`, `==`, truthiness) | `internal/pyrt` | Testado contra o Python operador a operador |
| `config_prodigy.py`, `build_opencore_efi` | — | Pendente |
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

## Módulo implementado: `internal/plist` (leitura e escrita de plist)

Porta o uso de `plistlib` do projeto (`utils.read_file` / `utils.write_file` para `.plist`, edição de `Info.plist` dos kexts e `config.plist`).

- `Marshal` escreve XML igual ao `plistlib.dumps(v, fmt=FMT_XML)`, byte a byte: cabeçalho, indentação com tabs, chaves ordenadas, `<real>` com o `repr` do Python, `<data>` quebrado em linhas de até 76 colunas, e `<dict/>`/`<array/>` vazios.
- `Unmarshal` lê XML com a mesma lógica do `_PlistParser`: `<key>` vazia tratada como falsa, `<integer>` com hexadecimal e underscores, elementos desconhecidos ignorados com seus filhos processados, e entidades `ENTITY` rejeitadas.
- `Dict` é um dicionário com ordem de inserção. O original depende da ordem (por exemplo, a última GPU de um relatório), então um `map` do Go não serviria.
- Inteiros fora de 64 bits viram `*big.Int`, como o `int` do Python. Não há overflow silencioso.

### Como a equivalência é verificada

`internal/plist/parity_test.go` roda `testdata/pyref_plist.py` com o `plistlib` original, sem modificações. Compara 38 casos de escrita (byte a byte) e 46 de leitura (valor a valor), incluindo os casos em que o Python lança erro. Os casos cobrem floats com fronteiras de `repr` (`1e15`, `1e16`, `1e-4`, `1e-5`, `inf`, `nan`), inteiros nas fronteiras de 64 bits, dados de 1, 57, 58 e 300 bytes, datas válidas e inválidas, e caracteres de escape.

### Diferenças conhecidas do `internal/plist`

1. **Padding Base64 no meio do dado**: o `binascii` do CPython continua decodificando depois de um `=` que aparece antes do fim (`QUJDRA==QQ==` gera `ABCD\x04\x10`). A regra exata não foi reproduzida. O `plistlib` nunca escreve esse formato, então só aparece em XML malformado. O caso fica fora da paridade.
2. **Plist binário (`bplist00`)**: não suportado ainda. A leitura retorna erro explícito, não uma interpretação errada.
3. **Parsing de inteiro duplicado**: `pyInt` em `internal/plist/pyfmt.go` repete a lógica de `parsePyInt` em `internal/macos`. Unificar em um pacote compartilhado é uma refatoração pendente, que não foi feita aqui.

## Pesquisa sobre a EFI automatizada: o que já foi confirmado

- **Downloads funcionam neste ambiente.** O índice de builds da Dortania (`raw.githubusercontent.com/dortania/build-repo/builds/latest.json`) responde 200, e o artefato do OpenCorePkg em `github.com/dortania/build-repo/releases/download/...` baixou 10,4 MB (200). A API do GitHub (`api.github.com`) e a página `releases/latest` retornam 403, então a resolução de versões precisa usar o índice da Dortania, como o próprio projeto faz.
- **Escopo do pipeline original** (`OpCore-Simplify.py:build_opencore_efi` e módulos chamados): `acpi_guru.py` (~3.300 linhas), `config_prodigy.py` (~750), `kext_maestro.py` (~810), `gathering_files.py` (~370), `smbios.py` (geração de serial via `macserial`), além dos datasets. A EFI completa depende de todos eles.

## Módulos de download e cache (`gathering_files.py`)

Pacotes: `internal/pyjson`, `internal/fsutil`, `internal/integrity`, `internal/fetch`, `internal/github` e `internal/gathering`. Cada um espelha o módulo Python de mesmo nome e tem um teste de paridade que roda o original.

### Como a equivalência é verificada

- **JSON** (`pyjson`): `Load` e `Dump` (indent=4, escape ASCII) comparados com `json.dumps(json.loads(...), indent=4)` em 24 documentos, incluindo erros de sintaxe.
- **Arquivos** (`fsutil`): `FindMatchingPaths` comparado com `find_matching_paths`, e os bytes de `WriteFile` comparados com `write_file` para `.json` e `.plist`.
- **Integridade** (`integrity`): manifests gerados e verificações após modificar, remover e incluir arquivos, mais os casos de pasta ausente e manifest inválido, comparados com `integrity_checker.py` sobre a mesma árvore.
- **Requisições** (`fetch`): 12 buscas e 7 downloads contra um servidor HTTP local, comparados com `resource_fetcher.py`. Cobrem gzip com e sem cabeçalho, deflate, JSON e plist inválidos, 404, 500, 204, SHA-256 correto, em maiúsculas, errado e ausente, arquivo vazio e uma falha que exige nova tentativa.
- **GitHub** (`github`): os parsers de payload, tag, corpo, assets e nome de asset comparados com `github.py` em 31 entradas. As entradas são HTML sintético com a estrutura que o parser lê. As URLs de `github.com` são fixas no original, então `get_latest_release` não é testado contra a página real.
- **Fluxo completo** (`gathering`): `gather_bootloader_kexts` executado pelo `gatheringFiles` original, com o índice da Dortania, o OpenCorePkg e o OcBinaryData servidos localmente. A comparação cobre resultado, árvore de arquivos com SHA-256, `history.json` byte a byte e o `manifest.json` como dicionário. Os 8 cenários: instalação nova, já atual, versão nova, SHA errado com cache válido, índice sem OpenCorePkg, URL ausente, 404 sem cache e SHA errado sem cache. Cada cenário cai no ramo pretendido, e o teste foi corrigido para isso.

### Diferenças conhecidas

1. **`process_kext` não portado.** É o `kext_maestro`, ainda não portado. Um kext baixado que contenha `.kext` que não seja `research`/`debug`/`Contents` falha com `ErrProcessKextNotPorted`, em vez de seguir sem processar. O ramo do OpenCorePkg não depende dele, e é o que está coberto.
2. **Instalação do macserial não coberta por teste.** O código existe e segue o original, mas o zip de teste não contém macserial, porque o Python grava em `Scripts/` fixo e não há como redirecioná-lo sem alterar o original.
3. **Ordem do manifest.** O Python grava o manifest na ordem do `os.walk`, que depende do sistema de arquivos. O Go grava em ordem alfabética. O conjunto de entradas é o mesmo, e a verificação usa o conjunto.
4. **Saída de terminal.** `head()` do original limpa o terminal e desenha uma caixa. A porta imprime as mensagens sem limpar a tela. A barra de progresso segue a mesma fórmula, mas não é comparada, porque depende do tempo.
5. **`chmod +x`.** O Go adiciona os bits de execução sem aplicar o `umask`, enquanto `chmod +x` respeita o `umask`.
6. **`splitlines`.** `splitLines` trata `\r\n`, `\r` e `\n`. O Python também trata separadores Unicode raros (como `\v` e `\x1c`), que não aparecem em HTML do GitHub.
7. **Windows do Hardware Sniffer.** `GatherHardwareSniffer` está implementado, mas o ramo Windows não roda aqui. Fora do Windows ele retorna sem fazer nada, como no original, e isso é testado.
8. **`GetKernelPatches`.** Quando `Kernel/Patch` existe mas não é uma lista, o Go trata como falha, e o Python devolveria o valor. Não acontece no arquivo de patches atual.
9. **Timeouts.** O Python usa `timeout=10` por operação de socket. O Go usa 10 s para conectar e para receber o cabeçalho, sem limite para o corpo.

## Módulo implementado: `internal/kextmaestro` (`kext_maestro.py`)

Pacotes: `internal/kextmaestro`, `internal/datasets` (gerado), `internal/pyrt`. O `pyrt` reproduz as operações do Python sobre o relatório de hardware: `dict.get`, `x in y` (que muda de significado conforme o tipo de `y`), `len`, `int()`, truthiness e `==`. Cada operação devolve erro onde o Python levantaria exceção, para que o port falhe nos mesmos casos.

### Como a equivalência é verificada

- **Seleção** (`SelectRequiredKexts`): 400 relatórios de hardware gerados com semente fixa, cobrindo ramos de Intel/AMD, laptop/desktop/NUC, GPUs Navi, Wi-Fi (Broadcom, Intel, Atheros, rtw88), Bluetooth, entradas de teclado/touchpad, SATA e USB com subsistemas, e versões do macOS de 16.7 a 25.0 mais uma inválida. Compara `needs_oclp` e o conjunto de kexts marcados. São 324 casos que completam e 76 que levantam erro nos dois lados.
- **Check, uncheck, status e compatibilidade**: 60 sequências aleatórias de 8 operações cada, com dependências, conflitos e force load, comparadas passo a passo e no estado final.
- **Verificação de força** (`VerifyKextCompatibility`): comparada com a resposta "yes" do prompt.
- **Bundles em disco**: fixtures com `Lilu.kext`, `AppleALC.kext` (correspondências PCI, IO name, USB e HDA), `VoodooTSCSync.kext` (sem executável), `itlwm.kext` (`WiFiConfig` vazio), um pasta sem `Info.plist` e um plist inválido. Comparados: `extract_pci_id`, `process_kext`, `modify_kexts` (dois ramos de versão e o caso sem escrita), `install_kexts_to_efi` (árvore resultante) e `load_kexts` (lista de `Kernel > Add`).
- **Fluxo completo com kext**: no teste de `gathering`, o Lilu é baixado como zip, passa por `process_kext` e vai para o cache, nos dois lados. Os arquivos e o histórico batem, incluindo o `Debug.kext` ignorado.
- **Operadores do Python** (`pyrt`): 60 casos de `in`, `==`, truthiness, `len` e `int()`, avaliados pelo Python real.
- **Sensibilidade**: trocando de propósito um `||` por `&&` na condição de Sandy/Ivy Bridge, a matriz de seleção falhou; restaurado, passa.

### Diferenças conhecidas

1. **Menu interativo** (`kext_configuration_menu`) não portado. É UI de terminal.
2. **Wi-Fi do `modify_kexts`**: com `WiFiConfig` preenchido, a porta precisa de `wifi_profile_extractor`, que depende de `netsh`, `nmcli` ou `networksetup`. Sem o gancho `WifiProfiles`, o caminho retorna `ErrWifiProfilesNotPorted` em vez de fingir sucesso.
3. **Saída de terminal e prompts de texto**: as mensagens `print` e a limpeza de tela não são reproduzidas. Os prompts (`request_input`) são, com a mesma sequência de respostas.
4. **Catálogo compartilhado**: o Python compartilha a lista de kexts entre instâncias de um mesmo processo. A porta dá uma cópia para cada `Maestro`. Na CLI do original há uma única instância, então o comportamento observável é igual.
5. **`int()` com dígitos não ASCII**: o Python aceita `٢٥`; o `pyrt` aceita só `0-9`. O caso fica fora da paridade.
6. **Dicionários do `pci_data`** (`SpoofGPUIDs`, `ChromebookIDs`, `ThinkPadTWX30IDs`, `UnsupportedNVMeSSDIDs`) ainda não foram gerados. Não são usados pelo `kext_maestro`, mas serão necessários no `config_prodigy`.
7. **Dois tipos de kext**: `gathering.Kext` e `datasets.KextInfo` representam o mesmo conceito, com campos sobrepostos. Unificar é uma refatoração pendente.

## Próxima etapa sugerida

1. `config_prodigy.genarate` sobre `internal/plist` e os resultados de `kextmaestro`, com paridade contra o original. Ele também precisa dos dicionários do `pci_data` mencionados acima.
2. Montagem da pasta EFI e limpeza de drivers e ferramentas não usados, como em `build_opencore_efi`.
3. `wifi_profile_extractor`, para fechar o gancho `WifiProfiles`.
4. `acpi_guru` por último, porque é o maior módulo.

Os passos 1 e 2 produzem uma EFI completa sem ACPI personalizado. O passo 4 é o que falta para a paridade total.

## Créditos

- [OpCore Simplify](https://github.com/lzhoang2801/OpCore-Simplify) (lzhoang2801), BSD 3-Clause.
- [OpenCorePkg](https://github.com/acidanthera/OpenCorePkg) (Acidanthera) e a [Dortania Guide](https://dortania.github.io/OpenCore-Install-Guide/), usados como referência pelo projeto original.
