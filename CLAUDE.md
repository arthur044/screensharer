# CLAUDE.md

Guia para o Claude Code trabalhar neste repositório, principalmente nos testes em tempo real no PC do dono do projeto (com celular de verdade). O plano completo está em `docs/PLANO.md`; leia as seções 2 e 4 antes de mudanças grandes.

## O que é

App desktop (Windows e Linux) que espelha um Android no PC, pelo USB ou Wi-Fi via adb, com controle por mouse e teclado.

```
celular: scrcpy-server v5.0.1 (enviado pelo adb)
   │  adb forward tcp:N → localabstract:scrcpy_<scid>
   ▼
Go (internal/scrcpy) ── media.Event ──▶ internal/stream (WebSocket 127.0.0.1, token) ──▶ frontend: WebCodecs → <canvas>
   ▲                                                                                          │
   └──────────── mensagens de controle ◀──── JSON media.Input ◀───────────────────────────────┘
```

- `main.go`, `app.go`: app Wails. Os métodos públicos de `App` são os bindings chamados pela interface.
- `internal/adb`: chamadas ao executável adb (devices, pair, connect, mdns, forward, Wi-Fi).
- `internal/scrcpy`: protocolo do scrcpy-server. `control.go` monta as mensagens PC→celular, `demux.go` lê o vídeo e as mensagens celular→PC, `session.go` sobe o servidor e conecta, `server.go` acha ou baixa o jar (SHA-256 fixado).
- `internal/stream`: WebSocket local. Descarta frames até o próximo keyframe quando o cliente atrasa.
- `internal/demo`: fonte de teste com ffmpeg (`testsrc2`), sem celular.
- `internal/media`: tipos comuns (`Source`, `Event`, `Input`) e utilitários de H.264.
- `frontend/src/main.ts`: tela de conexão. `mirror.ts`: vídeo, entrada e estatísticas. `keymap.ts`: teclas → keycodes Android. `selftest.ts`: decodifica um frame embutido na inicialização.
- `frontend/wailsjs/`: gerado pelo Wails (`wails generate module`). Não editar à mão; é regenerado em `wails dev`/`wails build` quando a assinatura de um método de `App` muda.

## Comandos

```sh
go test ./internal/...                                    # testes (rodam sem celular)
go test -race ./internal/...
cd frontend && npx tsc --noEmit                           # checagem de tipos do frontend
wails dev -tags webkit2_41 -appargs "--debug"             # Linux (sem a tag no Windows)
wails build -tags webkit2_41                              # build Linux
wails build -platform windows/amd64                       # build Windows (funciona também no Linux)
```

Com `--debug`, o log (`~/.config/screensharer/screensharer.log` ou `%AppData%\screensharer\screensharer.log`) recebe a cada 5 s as estatísticas da interface: fps, kbps, tempo de decodificação, fila e descartes. **Comece por esse log ao diagnosticar.**

## Convenções

- Textos da interface, mensagens de erro e comentários em **português**.
- Sem cgo além do que o Wails exige no Linux: o build Windows precisa continuar saindo do Linux com `GOOS=windows`.
- O protocolo do scrcpy muda entre versões e o servidor recusa versão diferente. Para atualizar `ServerVersion`, compare `control.go`/`demux.go` com `app/src/control_msg.c`, `app/src/device_msg.c` e `server/.../device/Streamer.java` da nova tag, e atualize `ServerSHA256` (vem do `install_release.sh` da tag).
- Mudou algo de protocolo ou de parser? Adicione um teste com os bytes esperados (os de `control_test.go` vêm dos testes oficiais do scrcpy).

## Já verificado (na sessão na nuvem, sem celular)

- Testes de protocolo batem byte a byte com os testes do scrcpy v5.0.1. A sessão foi testada com adb e celular simulados (`session_test.go`).
- App real no Linux (WebKitGTK 2.52, Xvfb, sem GPU) com a fonte de teste: 720×1600, 60 fps, decodificação de 4–7 ms, zero descartes. A tela de conexão renderiza com um adb falso.
- O build Windows compila no Linux (12 MB). **Nunca foi executado no Windows.**

## A verificar no PC real (lista de testes)

1. **Windows:** o app abre, o teste do decodificador dá "ok" e a fonte de teste roda a ~60 fps (se o ffmpeg estiver instalado).
2. **USB:** o aparelho aparece como "pronto". "Espelhar" mostra a tela e o download do scrcpy-server funciona na primeira vez.
3. **Controle:** toque, arrasto, rolagem, texto com acentos (ç, á), Enter/Backspace/setas, botões da barra, botão direito = voltar, Ctrl+V cola o texto do PC, Ctrl+C traz a seleção do celular.
4. **Rotação:** girar o celular troca a orientação do vídeo e o toque continua no lugar certo.
5. **Tela cheia:** F11, o botão e Esc, nos dois sistemas.
6. **"Ativar Wi-Fi":** descobrir se conecta direto ou se pede pareamento (está em aberto no plano, seção 2.4). Anotar o resultado e o modelo e a versão do Android.
7. **Wi-Fi:** pareamento por código, "Procurar" (mDNS), conectar e espelhar; desligar e ligar a tela do celular.
8. **Latência:** filmar um cronômetro na tela do celular junto com o monitor (seção 7 do plano). Meta p50: ≤ 80 ms no USB e ≤ 130 ms no Wi-Fi 5 GHz.
9. **Xiaomi:** o controle exige "Depuração USB (configurações de segurança)".

## Ajustes prováveis depois dos testes

- `MAX_DECODE_QUEUE` e `WARMUP_MS` em `mirror.ts`, e `queueSize` em `stream.go`, se aparecerem descartes ou atraso acumulado.
- O scrcpy aceita `video_codec_options` (por exemplo `latency=0` ou `low-latency:int=1`, Android 11+). Ainda não está ligado; testar aparelho por aparelho, porque alguns encoders recusam.
- Se algum encoder falhar em certas resoluções, tentar `max_size` menor (múltiplo de 16).
