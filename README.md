# screensharer

Espelha a tela de um celular Android no PC (Windows e Linux), pelo USB ou pelo Wi-Fi, com controle por mouse e teclado e com o som do celular tocando no PC. Usa o [`scrcpy-server`](https://github.com/Genymobile/scrcpy) (Apache-2.0) no celular, Go + [Wails](https://wails.io) no PC e WebCodecs para decodificar o vídeo.

O plano completo (decisões, riscos, custos e fases) está em [docs/PLANO.md](docs/PLANO.md).

## Requisitos

| | Windows | Linux |
|---|---|---|
| Rodar | [WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/) (já vem no Windows 10/11 atualizado) | `libwebkit2gtk-4.1-0`, `libgtk-3-0` **`gstreamer1.0-libav`** (decodificador H.264; no Arch, `gst-libav`) e **`gstreamer1.0-plugins-good`** (saída de som; no Arch, `gst-plugins-good`) |
| adb | [Android platform-tools](https://developer.android.com/tools/releases/platform-tools) no PATH, ou `--adb CAMINHO` | `sudo apt install adb` (já traz as regras udev) |
| Desenvolver | Go 1.25+, Node 20+ (22.18+ para os testes do frontend), CLI do Wails | igual, mais `libgtk-3-dev` e `libwebkit2gtk-4.1-dev` |

CLI do Wails: `go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0`. O `wails doctor` confere o que falta.

Na primeira vez que você espelhar um aparelho, o app baixa o `scrcpy-server` v5.0.1 do GitHub e confere o SHA-256. Sem internet, baixe o arquivo à mão e passe `--server CAMINHO`.

## Rodar

```sh
# desenvolvimento (recarrega a interface ao salvar)
wails dev -tags webkit2_41 -appargs "--debug"      # Linux
wails dev -appargs "--debug"                        # Windows

# build
wails build -tags webkit2_41                        # Linux  → build/bin/screensharer
wails build -platform windows/amd64                 # Windows → build/bin/screensharer.exe (também dá para compilar no Linux)
```

Sem o CLI do Wails também compila:

```sh
cd frontend && npm ci && npm run build && cd ..
go build -tags desktop,production,webkit2_41 -o build/bin/screensharer .                    # Linux
go build -tags desktop,production -ldflags "-H windowsgui" -o build/bin/screensharer.exe .  # Windows
```

Os executáveis de cada commit também ficam nos artefatos do CI (aba Actions do GitHub).

Opções de linha de comando:

| Opção | Para que serve |
|---|---|
| `--debug` | Logs detalhados e estatísticas na tela; as estatísticas também vão para o log |
| `--adb CAMINHO` | Usa um adb específico |
| `--server CAMINHO` | Usa um `scrcpy-server` já baixado |
| `--autostart demo` ou `--autostart SERIAL` | Já abre espelhando a fonte de teste ou um aparelho |

O log fica em `screensharer.log` na pasta de dados (`~/.config/screensharer` no Linux, `%AppData%\screensharer` no Windows).

## Usar

1. No celular, ative as **Opções do desenvolvedor** e a **Depuração USB**. Conecte o cabo e aceite a pergunta de depuração.
2. Clique em **Espelhar**.
3. Para usar sem cabo (Android 11+): com o cabo conectado, clique em **Ativar Wi-Fi**. Se o app pedir pareamento, abra no celular *Depuração sem fio → Parear dispositivo com código de pareamento* e digite o IP:porta e o código.

Na janela do vídeo:

| Ação | Atalho |
|---|---|
| Tela cheia | **F11** ou o botão ⛶ (Esc sai) |
| Voltar | Botão direito do mouse |
| Início | Botão do meio |
| Colar o clipboard do PC no celular | Ctrl+V |
| Copiar a seleção do celular para o PC | Ctrl+C |
| Estatísticas | Ctrl+Shift+D ou o botão ⓘ (só aparecem com o celular em pé) |

A barra de botões aparece quando o mouse chega ao topo da janela. Se o controle estiver desligado, o duplo clique também alterna a tela cheia.

O som do celular toca no PC (Android 11+) e o celular fica mudo enquanto isso. Dá para desligar em **Qualidade → Tocar o som do celular no PC**. Se a webview segurar o som no início, um clique na janela libera.

A janela assume a proporção do celular ao começar e a cada rotação, para não sobrar faixa preta, e volta ao tamanho anterior ao desconectar. Em tela cheia ou maximizada a proporção não bate; com o celular deitado, um botão na barra escolhe o que fazer: **Preencher** (corta as sobras), **Inteira** (com faixas) ou **Esticar**. Com o celular em pé a imagem aparece sempre inteira.

No Hyprland a janela fica flutuante durante o espelhamento (é o `hyprctl` que muda o tamanho dela) e volta ao estado anterior no fim. Precisa da configuração em Lua do Hyprland 0.5x.

## Testes

```sh
go test ./internal/...
go test ./internal/scrcpy -run=^$ -fuzz=FuzzReadVideo -fuzztime=30s
cd frontend && npx tsc --noEmit && npm test    # contas da janela e do toque
```

Sem celular, use **Abrir fonte de teste** (precisa do `ffmpeg` no PATH): ela gera vídeo sintético pelo mesmo caminho do celular.
