# Plano definitivo — screensharer

> Espelhar a tela do Android no PC (USB e Wi-Fi), com **um único pipeline de vídeo e transportes intercambiáveis**.
> Este documento parte do plano base, revisa cada fase pensando em riscos, desempenho, custo e viabilidade, e fecha com a versão definitiva das fases.

---

## 1. Veredito sobre o plano base

A ideia central (um pipeline e transportes trocáveis) está **certa e foi mantida**. A revisão encontrou cinco pontos que, sem ajuste, viram retrabalho:

| # | Ponto do plano base | Problema | Ajuste definitivo |
|---|---|---|---|
| 1 | `adb exec-out screenrecord` como fonte do MVP | Grava no máximo **180 s** na maioria das versões e depois o stream para. Não acompanha a **rotação** da tela. Não tem **áudio** nem **controle**. Não deixa pedir keyframe. O bitrate padrão é 20 Mbps | Usar `screenrecord` só num **spike de 1 a 2 dias**. A fonte de verdade da Fase 1 é o **`scrcpy-server`** (Apache-2.0), enviado pelo adb e sem instalar app no celular |
| 2 | Decodificar com PyAV e mostrar no OpenCV/PyQt | Decodificação por software, conversão YUV→BGR em NumPy e o GIL custam de 10 a 30 ms por frame e um núcleo de CPU. Além disso, a Fase 3 já prevê uma página web, então seriam **duas UIs** | **Uma UI só desde a Fase 1:** página web local que decodifica com **WebCodecs** (por hardware, no Chrome ou Edge). O backend só repassa o H.264 |
| 3 | Interface `start()` / `read_frame()` | O H.264 chega em NAL units, não em frames. A interface não prevê mudança de resolução na rotação, keyframe sob demanda, desconexão nem canal de controle (Fase 4) | Interface ampliada (seção 3.2) |
| 4 | Controle remoto na Fase 4 | Pelo `adb shell input tap` cada comando leva de 300 a 600 ms, o que é inutilizável. No app próprio (Fase 3), controlar exige AccessibilityService, que traz risco com a política da Play Store | Com o `scrcpy-server` o controle sai quase de graça: **sobe para a Fase 1** no modo ADB. No app próprio, fica só a visualização |
| 5 | WebSocket ou WebRTC na Fase 3 | WebRTC no Android aumenta o APK em ~15 MB e é bem mais complexo. Em LAN o ganho de latência é pequeno | **WebSocket binário + WebCodecs**. WebRTC só se um dia houver uso fora da LAN |

**Latência realista** (o plano base estimava 50–100 ms):

| Pipeline | USB | Wi-Fi 5 GHz |
|---|---|---|
| `screenrecord` + PyAV + OpenCV | 120–250 ms | 150–300 ms |
| `scrcpy-server` + WebCodecs (**definitivo**) | **40–80 ms** | 70–130 ms |

---

## 2. Arquitetura definitiva

```
Celular                                PC: app Wails, lado Go                      PC: app Wails, webview
──────────────────────────             ───────────────────────────                 ─────────────────────────
F1/F2  scrcpy-server (via adb) ─┐
F3     app Kotlin              ─┼──▶  Source (USB | WifiAdb | App)  ──WebSocket──▶  WebCodecs VideoDecoder (HW)
         MediaProjection        │      • parser H.264 (Annex B)     (127.0.0.1)     → <canvas> (WebGL)
         + MediaCodec H.264     │      • gravação (remux MP4, sem                   UI: dispositivos,
                                │        recodificar)                               qualidade, tela cheia
      ◀── controle (F1+) ───────┘      • canal de controle ◀───────────────────────  mouse/teclado
```

- **App: Wails v2 (Go)**, decisão do dono do projeto. É um executável só por sistema, com janela própria e a interface embutida.
  - **Comandos** (listar aparelhos, parear, conectar, configurar): a interface chama funções Go **diretamente** pelos bindings do Wails, sem HTTP.
  - **Vídeo:** o Go abre um **WebSocket em 127.0.0.1, numa porta aleatória e com token aleatório**. A interface recebe a URL por binding. Os eventos do Wails trafegam em JSON e não servem para 8 Mbps de dados binários.
- **Frontend: HTML + TypeScript + WebCodecs** (Vite, sem framework pesado). É a **mesma UI nas duas plataformas e em todas as fases**.
- **Motor da janela:** **WebView2** (Chromium do Edge) no Windows e **WebKitGTK 4.1** no Linux. Os dois foram testados (seção 2.5).

### 2.1 Linux e Windows: o que muda em cada um
O núcleo é o mesmo nos dois sistemas: o adb existe para ambos, o Go compila para ambos e a UI é web. As diferenças ficam só nas bordas:

| Item | Windows | Linux |
|---|---|---|
| adb | Embutir o `platform-tools` oficial para Windows | Embutir o `platform-tools` para Linux, ou usar o da distro |
| USB | Às vezes precisa do **driver USB do fabricante** (Samsung, Xiaomi). Google USB Driver para Pixel | Precisa de **regras udev** (`51-android.rules` ou o pacote `android-sdk-platform-tools-common`), senão o aparelho aparece como `no permissions` |
| Decodificação (WebCodecs) | WebView2: **por hardware** (D3D11), CPU < 5 % | WebKitGTK → GStreamer. **Exige um decodificador H.264 do GStreamer**: `gstreamer1.0-libav` (ou `openh264`/`vaapi`). Sem ele, `isConfigSupported` diz `true` e **a decodificação falha** (testado). Em software: ~6–7 ms por frame em 720p |
| Runtime da janela | **WebView2 Runtime**: já vem no Windows 10 e 11 atualizados; o instalador do Wails baixa se faltar | `libwebkit2gtk-4.1-0` + GStreamer: dependências do pacote `.deb` e verificadas na inicialização |
| Build | **Sem cgo**: compilado direto do Linux (`GOOS=windows`), testado | Com cgo: precisa de `libwebkit2gtk-4.1-dev` + `libgtk-3-dev`, build com a tag `webkit2_41` |
| Firewall (Fase 3) | Firewall do Windows pede permissão para a porta | Em geral liberado; `ufw` e `firewalld` exigem uma regra |
| Distribuição | `.exe` assinado (instalador NSIS do Wails ou zip portátil) | **`.deb`** (declara webkit2gtk, gstreamer1.0-libav e udev como dependências) + `.tar.gz`. AppImage não: empacotar WebKitGTK e GStreamer juntos é frágil |
| Wayland/X11 | — | O GTK funciona nos dois; testar a tela cheia em ambos |

**Como desenvolver e testar os dois:** programar no Linux; o build Windows do Wails sai na mesma máquina, sem cgo; o CI do GitHub Actions roda os testes em `ubuntu-latest` e `windows-latest` a cada PR. Os testes com USB de verdade precisam de **uma máquina Windows física** (driver e adb não dá para testar em CI). Reservar meio dia por fase para isso.

### 2.2 Backend: Node/TS é a melhor escolha?
**Não.** Ele funciona, mas não é a melhor opção para este projeto.

O backend aqui só faz entrada e saída: chama o `adb`, lê um socket, quebra o H.264 em pacotes, repassa por WebSocket e grava em MP4. **Qualquer linguagem aguenta isso com folga** (8 Mbps é pouquíssimo). Então o que decide é **distribuir para Windows e Linux sem dor**, a facilidade de manter e o que você já domina.

| Opção | Distribuição Win + Linux | Memória em uso | Facilidade | Pontos fracos |
|---|---|---|---|---|
| **Go** (recomendado) | **Binário único de ~10–15 MB sem runtime**, compilado para Windows direto do Linux | ~15–30 MB | Alta: linguagem pequena, concorrência simples (goroutines), biblioteca padrão forte em rede | Linguagem diferente do frontend (os tipos do protocolo ficam definidos em dois lugares) |
| Node + TS | Exige empacotar o runtime (~50–90 MB) com Node SEA ou pkg, que dá mais trabalho | ~60–100 MB | Alta para quem sabe JS; mesma linguagem do frontend | Empacotamento chato; `node_modules` pesado |
| Bun ou Deno + TS | `bun build --compile` / `deno compile` geram binário único (~60–100 MB), inclusive para Windows a partir do Linux | ~40–80 MB | Alta, mesmo TS do frontend | Runtimes mais novos, com compatibilidade menos testada no Windows |
| Python | PyInstaller, que é lento para abrir e **costuma ser marcado como vírus pelo antivírus no Windows** | ~40–80 MB | Muito alta | Distribuição é o ponto fraco; o GIL não chega a importar aqui |
| Rust | Binário único de ~5–10 MB | ~10–20 MB | Baixa: curva de aprendizado e desenvolvimento mais lento | Exagero para um backend que só repassa dados. Faria sentido só se a decodificação fosse nativa, fora do navegador |
| C# / .NET 8 | Single-file de ~30–70 MB | ~50–80 MB | Média-alta | Ecossistema mais forte no Windows; no Linux funciona, mas é menos comum |

**Decisão:**
- **Go**, se você topar aprender ou já souber. O diferencial é o binário único sem runtime para os dois sistemas, que é exatamente a dor deste projeto.
- **Bun ou Deno + TypeScript**, se preferir ficar só em TypeScript. Resolve a distribuição mantendo uma linguagem só.
- **Node puro e Python** ficam descartados como produto final por causa da distribuição. Para o spike da Fase 0, qualquer um serve.

### 2.3 Janela: por que Wails
| Opção | Situação |
|---|---|
| **Wails v2** | **Escolhido.** Janela nativa com bindings Go↔JS, ~12–15 MB. No Linux, depende do GStreamer com H.264 (mitigado em 2.5) |
| Electron | Descartado: ~100 MB e um segundo runtime (Node) ao lado do Go |
| Navegador em modo app | Fica só como **modo de diagnóstico** (`--browser`): a mesma UI servida em 127.0.0.1, útil se a webview de alguma máquina der problema |
| Tauri | Mesmo motor do Wails no Linux, mas com backend em Rust |
| Wails v3 | Ainda não é a versão estável; migrar quando sair, se trouxer vantagem |

### 2.4 Janela e interface (requisitos do dono do projeto)
- **Janela própria com tela cheia.** A janela é do Wails, e a tela cheia é **nativa** (`runtime.WindowFullscreen` / `WindowUnfullscreen`). Para entrar: **F11**, **duplo clique no vídeo** ou um botão; `Esc` sai.
  - Essas teclas são tratadas pelo próprio app, então funcionam igual no WebView2 e no WebKitGTK.
  - As demais teclas vão para o celular quando o controle estiver ligado.
  - O app lembra o tamanho e a posição da janela.
- **Tela de conexão** (é a primeira tela que aparece):
  1. **USB:** lista os aparelhos em tempo real (`adb track-devices`) com o estado de cada um (`device`, `unauthorized` → "aceite a depuração no celular", `no permissions` → instrução de udev no Linux). Um botão **Espelhar** em cada aparelho.
  2. **Wi-Fi:**
     - Botão **"Ativar Wi-Fi para este aparelho"**: com o celular no USB, liga a depuração sem fio sem precisar digitar código. Caminho: `adb shell settings put global adb_wifi_enabled 1`, ler o IP e a porta, depois `adb connect`. **A confirmar no aparelho real** se a chave já autorizada pelo USB dispensa o pareamento no TLS. Se não dispensar, o botão cai para o formulário de pareamento. `adb tcpip 5555` continua proibido (R7).
     - Formulário **Parear** (IP:porta + código de 6 dígitos → `adb pair`).
     - Lista **"Encontrados na rede"** (`adb mdns services`) com o botão **Conectar**.
  3. Configurações: resolução máxima, FPS, bitrate, ligar ou desligar o controle e o áudio.
- **Na janela do vídeo:** barra discreta (que some depois de 2 s) com tela cheia, rotação, qualidade, desconectar e o overlay de estatísticas (`Ctrl+Shift+D`).

### 2.5 Viabilidade do Go + Wails: verificada na prática
Foi feito um spike (`cmd/spike-webcodecs`): o Go gera H.264 com o ffmpeg, separa as access units e envia por WebSocket para uma página com WebCodecs. Resultado neste ambiente (Linux na nuvem):

| Item medido | Resultado |
|---|---|
| Entrega pelo WebSocket | **60 fps** estáveis a 8,7 Mbps (720p60), pior intervalo entre frames de 22 ms |
| Memória do servidor Go | **12 MB** (RSS) |
| Binário Linux / Windows (`CGO_ENABLED=0`, compilado aqui) | 8,6 MB / **5,9 MB** |
| Dependências | 1 biblioteca pura em Go (`github.com/coder/websocket`); **sem cgo**, então a compilação cruzada para Windows é trivial |
| Decodificação H.264 no Chromium do ambiente | Build sem codecs proprietários (`false`). Não afeta o Wails |

**Teste com o Wails v2.16 no Linux** (Ubuntu 24.04, WebKitGTK 2.52.6, Xvfb sem GPU), recebendo o mesmo stream pelo WebSocket:

| Item medido | Resultado |
|---|---|
| WebCodecs disponível | Sim: `VideoDecoder` existe; H.264 Baseline/High, H.265, VP8 e VP9 reportados como suportados |
| Decodificação H.264 720p60 **com** `gstreamer1.0-libav` | **625 frames em 10 s (~60 fps), 6,6 ms por frame** (software, sem GPU), zero erros |
| **Sem** `gstreamer1.0-libav` | `isConfigSupported` → `true`, mas cada frame dá `EncodingError: Decode error` |
| WebSocket da origem `wails://wails` para 127.0.0.1 | Funciona, **desde que o servidor aceite essa origem** (a `coder/websocket` recusa origem diferente por padrão) |
| Binário Linux (cgo, webkit2_41) / Windows (sem cgo, compilado no Linux) | 14,5 MB / **11,9 MB** |

O WebView2 do Windows usa o motor do Edge, que tem H.264 por hardware. **Confirmar no seu PC Windows** com o app real.

**Veredito: Go + Wails é viável nos dois sistemas.** Tudo que é pesado (decodificar e desenhar o vídeo) fica com a webview. O Go só faz entrada e saída, que é o ponto forte dele. O cgo só aparece no build do Linux, que é feito no próprio Linux.

**Onde o Go NÃO seria viável:** fazer a janela e decodificar o vídeo em Go puro. Seria preciso usar FFmpeg e SDL via cgo, que quebra a compilação cruzada simples e exige mingw para gerar o build de Windows. As bibliotecas de interface em Go (Fyne, Gio) não têm decodificador de vídeo. **Por isso a janela é uma webview (Wails).**

**Mitigação obrigatória no Linux** (o `isConfigSupported` não é confiável):
1. Na inicialização, o app **decodifica um frame H.264 de teste embutido no binário** (poucos KB). Se falhar, mostra: "Instale o decodificador de vídeo: `sudo apt install gstreamer1.0-libav`" (com o comando de cada distro: apt, dnf, pacman).
2. O `.deb` declara `gstreamer1.0-libav` como dependência, então quem instala pelo pacote nunca cai nesse caso.
3. Se a webview falhar por outro motivo, o modo `--browser` abre a mesma UI no Chrome ou no Edge.

**Segurança do WebSocket local:** escuta só em 127.0.0.1, numa porta aleatória, aceita só as origens do Wails (`wails://wails` no Linux, `http://wails.localhost` no Windows) e exige um token aleatório gerado a cada execução. Assim, nenhum site aberto no navegador consegue se conectar ao stream.

### 2.6 Como fica o desenvolvimento entre a nuvem e o seu PC
| Onde | O que faz |
|---|---|
| Sessão na nuvem (aqui) | Código, testes unitários, fuzzing do parser, builds de Linux e Windows, CI |
| Seu PC + Claude Code local | `git pull` → `wails dev` (recarrega a interface na hora) com o celular conectado: testes em tempo real, medição de latência, ajustes finos por aparelho |

Para facilitar essa passagem:
- Requisitos no PC: **Go 1.24+**, **Node 20+** (só para o build do frontend) e o **CLI do Wails** (`go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0`). No Linux, também `libwebkit2gtk-4.1-dev`, `libgtk-3-dev` e `gstreamer1.0-libav`. O `wails doctor` confere tudo. O adb pode ser o do sistema ou um caminho dado por `--adb`. O `scrcpy-server` é baixado na primeira execução **com checagem do SHA-256 fixado no código**: o download do GitHub está bloqueado neste ambiente, mas no seu PC funciona.
- `--debug` liga logs detalhados e o overlay de estatísticas, para o Claude no seu PC diagnosticar com dados reais.
- Um `CLAUDE.md` na raiz vai explicar a arquitetura, os comandos e o que testar, para o Claude local continuar sem você precisar repetir o contexto.

### Por que WebCodecs no lugar de PyAV/OpenCV
- Decodifica por hardware onde o sistema permite (D3D11 no Windows; no Linux, GStreamer, veja 2.1) e entrega um `VideoFrame` direto para o canvas.
- A webview cuida do vsync e do redimensionamento.

---

## 3. Código

### 3.1 Estrutura do repositório
```
main.go, app.go  app Wails (bindings expostos para a UI)
/internal      pacotes Go
  /source      UsbSource, WifiAdbSource, AppSource, ScreenrecordSource (só para o spike)
  /protocol    parser H.264, formato dos pacotes, mensagens de controle
  /record      remux para MP4 sem recodificar (biblioteca de MP4 em Go, ou ffmpeg -c copy)
  /platform    diferenças por SO: localizar adb, udev/driver, checagem do GStreamer, firewall
  /stream      servidor WebSocket local (token + origens do Wails)
/build         ícones e configuração de empacotamento do Wails (NSIS, .deb)
/frontend      Vite + TS: decoder, renderização, telas de conexão e vídeo, captura de input
/android       app Kotlin (Fase 3)
/vendor        scrcpy-server.jar (versão fixada + NOTICE da licença)
/docs
```

### 3.2 Interface das fontes (substitui `start()` / `read_frame()`)
```go
type VideoSource interface {
    Start(ctx context.Context, cfg Config) (StreamInfo, error) // Config: MaxSize, FPS, Bitrate, Codec
    Stop() error
    Packets() <-chan EncodedPacket   // uma access unit por pacote
    Events() <-chan Event            // Config (SPS/PPS novos), Disconnected, Error
    RequestKeyframe()                // depois de perda, reconexão ou decoder novo
}
type BitrateSetter interface{ SetBitrate(bps int) } // opcional: taxa adaptativa
type Controller   interface{ Control() ControlChannel } // opcional: o AppSource não implementa

type EncodedPacket struct { Data []byte; PTSMicros int64; Keyframe bool }
type StreamInfo    struct { Width, Height int; CodecString string } // ex.: "avc1.42E01F"
```
O evento `Config` dispara quando chegam SPS/PPS novos, por exemplo numa rotação. A UI então reconfigura o `VideoDecoder`.

### 3.3 Regras de desempenho (obrigatórias em todas as fontes)
1. **Descartar em vez de enfileirar.** Se o WebSocket ou o decoder atrasar, descarta tudo até o próximo keyframe. Atraso acumulado é o pior defeito desse tipo de app.
2. Usar `optimizeForLatency: true` no `VideoDecoder` e nenhum buffer de jitter no USB.
3. No encoder: sem B-frames, `KEY_LOW_LATENCY` (Android 11+), I-frame a cada ~10 s e keyframe sob demanda.
4. Resolução padrão de **1080p a 60 fps e 8 Mbps**. No Wi-Fi 2.4 GHz, cair para 720p, 30 fps e 3 Mbps.
5. Dimensões em múltiplos de 16, porque encoders da Samsung e da MediaTek falham com tamanhos ímpares.

---

## 4. Fases definitivas

### Fase 0: Spike do pipeline (1 a 2 dias)
- `adb exec-out screenrecord --output-format=h264 --bit-rate 8M --size 1280x720 -` → backend → WebSocket → WebCodecs.
- **Objetivo:** validar o caminho do backend até a UI e medir a latência de referência. **Não é produto.**
- **Saída:** vídeo aparecendo na janela do Wails (Windows e Linux) e latência medida (seção 7).

### Fase 1: MVP via USB (2 a 3 semanas)
- `UsbSource` com o `scrcpy-server`: `adb push`, `app_process`, `adb forward` e leitura dos sockets de vídeo e controle.
- Detecção de aparelhos com `adb track-devices` (conectar e desconectar com hot-plug).
- **Controle por mouse e teclado + clipboard**, adiantado da Fase 4 porque o canal já vem pronto.
- Reconexão automática, keyframe na reconexão e tratamento de rotação.
- **Saída:** latência p50 ≤ 80 ms; sessão de 2 horas sem atraso acumulado nem vazamento de memória; testado em pelo menos 3 fabricantes.

### Fase 2: Wi-Fi via ADB (1 semana)
- Pareamento pela UI: o usuário digita o código e o backend roda `adb pair`.
- **Descoberta já resolvida pelo adb:** `adb mdns services` lista `_adb-tls-connect._tcp`, então não é preciso implementar mDNS próprio. Isso importa porque **a porta muda a cada reconexão**.
- `WifiAdbSource` = `UsbSource` com outro serial, que era a premissa do plano base e se confirma.
- Taxa adaptativa (bitrate e FPS) guiada pela fila de envio e pelo RTT, e indicador de qualidade na UI.
- **Saída:** p50 ≤ 130 ms no 5 GHz; sobrevive à tela do celular apagando e acendendo (reconecta sozinho).

### Fase 3: App próprio, Wi-Fi sem ADB (5 a 7 semanas). **Só começar depois de usar as Fases 1 e 2 de verdade**
- App Kotlin: MediaProjection + MediaCodec H.264 + foreground service do tipo `mediaProjection` (obrigatório no Android 14+).
- O **celular conecta no PC**: a UI web mostra um QR code com `ws://IP:porta` + token de pareamento + fingerprint do certificado.
- Transporte: WebSocket binário sobre TLS com o certificado fixado pelo QR. Assim a criptografia funciona sem CA, porque a interface do Wails só fala com o Go local.
- **Somente visualização.** Controle continua no modo ADB.
- **Saída:** p50 ≤ 150 ms; o consentimento por sessão do Android 14+ fica explicado na UI.

### Fase 4: Extras (cada um entra de forma independente)
| Extra | Modo ADB | App próprio | Custo |
|---|---|---|---|
| Áudio interno (Android 11+) | `scrcpy-server` já captura (Opus) | `AudioPlaybackCapture` (apps podem bloquear) | Baixo / médio |
| Gravação | Remux do H.264 para MP4 **sem recodificar**, com CPU quase zero | Igual | Baixo |
| Qualidade e FPS na UI | Parâmetros do servidor | Parâmetros do MediaCodec | Baixo |
| Vários aparelhos ao mesmo tempo | Uma `Source` por serial | Igual | Médio |

---

## 5. Riscos (P = probabilidade, I = impacto: A/M/B)

| # | Risco | P | I | Mitigação |
|---|---|---|---|---|
| R1 | `screenrecord` para em 180 s e não gira com a tela | A | A | Restrito ao spike. Produto usa `scrcpy-server` |
| R2 | Atualizações do Android quebram as APIs ocultas que o `scrcpy-server` usa | M | A | Fixar a versão no `/vendor`; acompanhar os releases do upstream; matriz de testes por versão |
| R3 | **Isolamento de clientes no Wi-Fi** (rede de visitantes, corporativa, alguns roteadores) impede o PC e o celular de se enxergarem | M | A | Detectar e explicar na UI; USB como alternativa sempre disponível |
| R4 | ADB sem fio exige Android 11+ e a mesma rede (já estava no plano base) | A | M | Android 10 ou anterior: `adb tcpip` só **temporário**, iniciado por USB e com aviso de segurança |
| R5 | Wi-Fi 2.4 GHz congestionado gera latência e travadas (já estava no plano base) | A | M | Perfil automático de 720p30, indicador de qualidade, recomendar 5 GHz |
| R6 | Restrições de fabricante (já estava no plano base). Na **Xiaomi** o controle exige ativar "Depuração USB (configurações de segurança)", que pede conta Mi e chip | A | M | Assistente por fabricante na UI. O vídeo funciona mesmo sem o controle |
| R7 | Apps com `FLAG_SECURE` (bancos, streaming) aparecem pretos | A | B | Não contornar (questão legal e de termos de uso). Avisar na UI |
| R8 | Windows: falta de driver USB do fabricante, ou conflito de versão de `adb` com outro instalado (Android Studio) | M | M | Distribuir o próprio `platform-tools` e usar uma porta de servidor adb própria (`ANDROID_ADB_SERVER_PORT`); link para o driver OEM |
| R9 | Fase 3: WebSocket sem autenticação deixaria qualquer pessoa da rede ver a tela | M | **A** | Token e certificado fixado pelo QR; o backend escuta a UI **só em 127.0.0.1** |
| R10 | Fase 3: o Android 14+ pede consentimento **a cada sessão**, e alguns fabricantes matam o foreground service | A | M | Comportamento do sistema, não tem como evitar. Explicar na UI e pedir para tirar o app da otimização de bateria |
| R11 | Firewall do Windows bloqueia a porta da Fase 3 | A | B | Regra criada pelo instalador ou instrução na primeira execução |
| R12 | Aquecimento e bateria em sessões longas | M | M | Cair para 30 fps com a tela parada; perfil econômico |
| R15 | Linux: sem regra udev o aparelho aparece como `no permissions` | A | M | O AppImage detecta isso e mostra o comando para instalar a regra; documentar o pacote da distro |
| R16 | Linux: decodificação por software (sem VAAPI no GStreamer) | A | B | ~6–7 ms por frame em 720p, medido; 1080p60 cabe num núcleo; perfil 720p para máquinas fracas. VAAPI via `gstreamer1.0-vaapi` quando houver |
| R18 | Linux: GStreamer **sem decodificador H.264** (sem `gstreamer1.0-libav`). O `isConfigSupported` mente e a decodificação falha (testado) | M | A | Frame de teste na inicialização + mensagem com o comando de instalação; dependência no `.deb`; modo `--browser` como alternativa |
| R19 | Diferenças entre WebView2 e WebKitGTK (eventos de teclado, tela cheia, desempenho do canvas) | M | M | Mesmos testes manuais nos dois a cada fase; tela cheia nativa do Wails; evitar APIs exclusivas do Chromium (Keyboard Lock) |
| R20 | Windows sem o WebView2 Runtime (Windows 10 antigo ou sem atualizações) | B | M | O instalador NSIS do Wails baixa o runtime |
| R17 | Projeto desenvolvido no Linux e Windows testado pouco | M | A | CI com `windows-latest` + teste manual em Windows físico por fase |
| R13 | Licenças | B | A | `scrcpy-server` é Apache-2.0 (manter NOTICE). Não usar código GPL (UxPlay, ffmpeg compilado com GPL) dentro do produto |
| R14 | **Produto:** o scrcpy já faz as Fases 1, 2 e 4 de graça | A | A* | *Só pesa se o objetivo for lançar um produto. Diferenciais: UI sem terminal, pareamento por QR, vários aparelhos e a Fase 3 sem ADB |

---

## 6. Custo e viabilidade

### 6.1 Custo
| Item | Valor |
|---|---|
| Infraestrutura | **R$ 0**. Tudo roda local, sem servidor nem nuvem |
| Bibliotecas | R$ 0 (adb, scrcpy-server, Go e WebCodecs são gratuitos ou abertos) |
| Aparelhos de teste (2 a 3 usados: Samsung, Xiaomi, Motorola) | ~R$ 1.500–2.500, uma vez (opcional se já tiver) |
| Google Play (Fase 3) | US$ 25, uma vez (ou distribuir o APK direto, de graça) |
| Assinatura de código no Windows (só se distribuir o instalador) | ~US$ 10/mês (Azure Trusted Signing) |
| Linux (AppImage/tar.gz) | R$ 0; não exige assinatura |
| CI para Linux + Windows (GitHub Actions) | Grátis em repositório público; em privado, a cota grátis costuma bastar (minutos de Windows contam em dobro) |
| Espelhar pela internet (fora do escopo) | Precisaria de relay TURN: ~US$ 0,20–0,30 por hora por usuário a 8 Mbps |

### 6.2 Esforço (1 desenvolvedor)
| Fase | Prazo | Acumulado |
|---|---|---|
| 0 Spike | 1–2 dias | ~0,5 semana |
| 1 USB (Linux + Windows) | 3–4 semanas | ~4 semanas |
| 2 Wi-Fi ADB | 1 semana | ~5 semanas |
| 3 App próprio | 5–7 semanas | ~11 semanas |
| 4 Extras | 1–2 semanas | ~13 semanas |

Suportar os dois sistemas soma ~1 semana no total (empacotamento, udev, driver, testes no Windows), quase toda na Fase 1. Somar +1–2 semanas se você ainda não souber Go.

### 6.3 Viabilidade por fase
| Fase | Técnica | Observação |
|---|---|---|
| 0, 1 e 2 | **Alta** | Caminho já provado (scrcpy). O risco está nos fabricantes, não na tecnologia |
| 3 | **Média** | Consentimento por sessão, foreground service e fabricantes que encerram processos |
| 4: controle no modo ADB | **Alta** | Vem do `scrcpy-server` |
| 4: controle no app próprio | **Baixa a média** | AccessibilityService com risco na Play Store. Fica fora do plano |

---

## 7. Medição e testes
- **Primeiro teste no seu PC:** rodar o app com `--debug` e, sem celular conectado, usar a fonte de teste interna (vídeo sintético). O overlay deve mostrar ~60 fps e o tempo de decodificação. Repetir no Windows e no Linux.
- **Latência real:** cronômetro em milissegundos na tela do celular, filmando o celular e o monitor juntos com o celular a 240 fps; média de 30 amostras. Repetir em USB, 5 GHz e 2.4 GHz.
- **Overlay de debug na UI:** fps, bitrate, frames descartados, tamanho da fila e RTT.
- **Testes de longa duração:** 2 horas de sessão, acompanhando o uso de memória do backend e da aba.
- **Testes automáticos:** parser H.264 (fuzzing nativo do Go, `go test -fuzz`, com dados corrompidos) e protocolo de controle, rodando no CI do GitHub Actions em **Ubuntu e Windows**.
- **Teste manual em Windows físico** a cada fase: driver USB, adb embutido, WebView2 e firewall.

## 8. Decisões tomadas (definitivo)
1. Pipeline único com `VideoSource` intercambiável: **mantido** do plano base.
2. Fonte da Fase 1: **`scrcpy-server`**. O `screenrecord` fica só no spike.
3. UI e decodificação: **página web local + WebCodecs** em todas as fases, no lugar de PyAV/OpenCV.
4. Backend: **Go**, com binário único para Windows e Linux. Alternativa: **Bun ou Deno + TypeScript** compilados. Node puro e Python ficam só no spike.
5. Transporte da Fase 3: **WebSocket + TLS fixado por QR**. WebRTC fica descartado enquanto o uso for só em LAN.
6. Controle remoto e clipboard: **sobem para a Fase 1** no modo ADB. No app próprio, só visualização.
7. Fase 3 só começa depois de usar as Fases 1 e 2 no dia a dia.
8. Plataformas: **Windows e Linux por igual, desde a Fase 1**. Distribuição: `.exe` assinado (NSIS) + `.deb`/`.tar.gz`.
9. Janela: **Wails v2** (decisão do dono do projeto), com tela cheia nativa (F11, duplo clique ou botão) e tela de conexão USB/Wi-Fi como primeira tela (seção 2.4). O navegador fica só como modo `--browser` de diagnóstico.
