# CLAUDE.md

Guia para o Claude Code trabalhar neste repositório, principalmente nos testes em tempo real no PC do dono do projeto (com celular de verdade). O plano completo está em `docs/PLANO.md`; leia as seções 2 e 4 antes de mudanças grandes.

## O que é

App desktop (Windows e Linux) que espelha um Android no PC, pelo USB ou Wi-Fi via adb, com controle por mouse e teclado.

```
celular: scrcpy-server v5.0.1 (enviado pelo adb)
   │  adb forward tcp:N → localabstract:scrcpy_<scid>
   ▼
Go (internal/scrcpy) ── media.Event ──▶ internal/stream (WebSocket 127.0.0.1, token) ──▶ frontend: WebCodecs → <canvas>, Web Audio
   ▲                                                                                          │
   └──────────── mensagens de controle ◀──── JSON media.Input ◀───────────────────────────────┘
```

- `main.go`, `app.go`: app Wails. Os métodos públicos de `App` são os bindings chamados pela interface.
- `internal/adb`: chamadas ao executável adb (devices, pair, connect, mdns, forward, Wi-Fi).
- `internal/scrcpy`: protocolo do scrcpy-server. `control.go` monta as mensagens PC→celular, `demux.go` lê o vídeo, o áudio e as mensagens celular→PC, `session.go` sobe o servidor e conecta, `server.go` acha ou baixa o jar (SHA-256 fixado).
- `internal/stream`: WebSocket local. Descarta frames até o próximo keyframe quando o cliente atrasa.
- `internal/demo`: fonte de teste com ffmpeg (`testsrc2`), sem celular.
- `internal/media`: tipos comuns (`Source`, `Event`, `Input`) e utilitários de H.264.
- `internal/platform`: o que muda por sistema (achar o adb, pasta de dados, esconder o console no Windows) e o ajuste da janela no Hyprland (`hyprland.go`).
- `frontend/src/main.ts`: tela de conexão. `mirror.ts`: vídeo, entrada, janela e estatísticas. `audio.ts`: toca o som do celular (PCM de 48 kHz estéreo, sem compressão) com um AudioWorklet e uma fila de 60 ms. `fit.ts`: contas puras do tamanho da janela e das coordenadas do toque (testadas em `frontend/test`). `keymap.ts`: teclas → keycodes Android. `selftest.ts`: decodifica um frame embutido na inicialização.
- `frontend/wailsjs/`: gerado pelo Wails (`wails generate module`). Não editar à mão; é regenerado em `wails dev`/`wails build` quando a assinatura de um método de `App` muda.

## Comandos

```sh
go test ./internal/...                                    # testes (rodam sem celular)
go test -race ./internal/...
cd frontend && npx tsc --noEmit && npm test               # tipos e testes do frontend (Node 22.18+)
wails dev -tags webkit2_41 -appargs "--debug"             # Linux (sem a tag no Windows)
wails build -tags webkit2_41                              # build Linux
wails build -platform windows/amd64                       # build Windows (funciona também no Linux)

# sem o CLI do Wails:
cd frontend && npm ci && npm run build && cd ..
go build -tags desktop,production,webkit2_41 -o build/bin/screensharer .               # Linux
go build -tags desktop,production -ldflags "-H windowsgui" -o build/bin/screensharer.exe .   # Windows
```

Com `--debug`, o log (`~/.config/screensharer/screensharer.log` ou `%AppData%\screensharer\screensharer.log`) recebe a cada 5 s as estatísticas da interface (fps, kbps, tempo de decodificação, fila e descartes) e uma linha a cada ajuste da janela. **Comece por esse log ao diagnosticar.**

## Convenções

- Textos da interface, mensagens de erro e comentários em **português**.
- Sem cgo além do que o Wails exige no Linux: o build Windows precisa continuar saindo do Linux com `GOOS=windows`.
- O protocolo do scrcpy muda entre versões e o servidor recusa versão diferente. Para atualizar `ServerVersion`, compare `control.go`/`demux.go` com `app/src/control_msg.c`, `app/src/device_msg.c` e `server/.../device/Streamer.java` da nova tag, e atualize `ServerSHA256` (vem do `install_release.sh` da tag).
- Mudou algo de protocolo ou de parser? Adicione um teste com os bytes esperados (os de `control_test.go` vêm dos testes oficiais do scrcpy).

## Já verificado

- **Protocolo:** os testes batem byte a byte com os do scrcpy v5.0.1 (SHA-256 do servidor, mensagens de controle, mensagens do celular e cabeçalho de vídeo). A sessão é testada com adb e celular simulados (`session_test.go`).
- **Windows 11 + Galaxy A71 (SM-A715F, Android 13), em 12/10/2026:** o app abre e decodifica; USB espelha em 864×1920 com decodificação de ~1 ms, sem descartes, pico de 51 fps e 13,5 Mbps; rotação e tela cheia funcionam; pareamento por código e conexão pelo Wi-Fi funcionam. Controle: "maioria dos testes ok", sem detalhe por item.
- **Linux (Omarchy, Hyprland 0.56, WebKitGTK 2.52) + o mesmo A71, em 12/10/2026:** USB espelha com decodificação de 4–7 ms, sem descartes; a janela assume a proporção do celular em pé e deitado; o botão de preenchimento aparece só deitado.
- O build Windows compila no Linux.

## O que os aparelhos reais ensinaram

- **"Ativar Wi-Fi" pede pareamento** no A71 com Android 13: a autorização do USB não vale para o Wi-Fi. O shell também não lê `service.adb.tls.port`; a porta vem do anúncio mDNS.
- **Não pedir keyframe (`RESET_VIDEO`) antes de o vídeo começar:** o scrcpy-server v5.0.1 morre com `NullPointerException`. Quando o espelhamento cai, o motivo real costuma estar no celular: `adb logcat -d -s scrcpy:V AndroidRuntime:E`.
- **WebKitGTK não acompanha a troca de resolução:** depois de uma rotação o decoder continua entregando o tamanho antigo, com a imagem espremida. Por isso `mirror.ts` recria o decoder a cada mensagem `config`.
- **Som no Linux:** o Web Audio do WebKitGTK precisa do `autoaudiosink`, do `gst-plugins-good`. Sem ele o AudioContext fica suspenso e `resume()` falha com "Failed to start the audio device"; o app mostra o pacote a instalar.
- **Hyprland:** o compositor marca toda janela como em mosaico, o GTK então ignora `WindowSetSize` e `WindowIsMaximised` devolve `true` sempre. O tamanho é pedido ao Hyprland pelo `hyprctl` (`internal/platform/hyprland.go`, sintaxe Lua do 0.5x): a janela fica flutuante durante o espelhamento e volta ao estado anterior no fim.

## A verificar no PC real

1. **Windows com a janela nova (PR #2):** o ajuste da janela veio de código validado lá, mas o botão de preenchimento, a barra que quebra em linhas e a recriação do decoder a cada `config` ainda não rodaram no Windows. Conferir descartes e atraso: cada pedido de keyframe agora recria o decoder.
2. **Som (branch `audio-no-pc`):** no Linux com o A71 os blocos chegam e a fila fica em 45–60 ms sem falhas, mas falta conferir de ouvido a sincronia com o vídeo, o atraso, o Wi-Fi e o Windows (o WebView2 pode exigir um clique antes de tocar).
3. **Controle, item por item:** toque, arrasto, rolagem, texto com acentos (ç, á), Enter/Backspace/setas, botões da barra, botão direito = voltar, Ctrl+V cola o texto do PC, Ctrl+C traz a seleção do celular.
4. **Janela:** parar e espelhar de novo o mesmo aparelho (a janela deve se ajustar outra vez); girar o celular em tela cheia e sair dela; no Hyprland, a janela deve voltar ao mosaico ao desconectar.
5. **Fonte de teste** com ffmpeg no Windows (~60 fps).
6. **Wi-Fi:** desempenho (fps, descartes) e desligar e ligar a tela do celular.
7. **Latência:** filmar um cronômetro na tela do celular junto com o monitor (seção 7 do plano). Meta p50: ≤ 80 ms no USB e ≤ 130 ms no Wi-Fi 5 GHz.
8. **Xiaomi:** o controle exige "Depuração USB (configurações de segurança)".
9. **Outros ambientes Linux** (GNOME, KDE): o ajuste da janela só foi visto no Hyprland.

## Ajustes prováveis depois dos testes

- `MAX_DECODE_QUEUE` e `WARMUP_MS` em `mirror.ts`, e `queueSize` em `stream.go`, se aparecerem descartes ou atraso acumulado.
- O scrcpy aceita `video_codec_options` (por exemplo `latency=0` ou `low-latency:int=1`, Android 11+). Ainda não está ligado; testar aparelho por aparelho, porque alguns encoders recusam.
- Se algum encoder falhar em certas resoluções, tentar `max_size` menor (múltiplo de 16).
