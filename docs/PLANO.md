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
Celular                                PC: backend local (Node/TS)                 PC: navegador (localhost)
──────────────────────────             ───────────────────────────                 ─────────────────────────
F1/F2  scrcpy-server (via adb) ─┐
F3     app Kotlin              ─┼──▶  Source (USB | WifiAdb | App)  ──WebSocket──▶  WebCodecs VideoDecoder (HW)
         MediaProjection        │      • parser H.264 (Annex B)     (localhost)     → <canvas> (WebGL)
         + MediaCodec H.264     │      • gravação (remux MP4, sem                   UI: dispositivos,
                                │        recodificar)                               qualidade, tela cheia
      ◀── controle (F1+) ───────┘      • canal de controle ◀───────────────────────  mouse/teclado
```

- **Backend: Node.js + TypeScript**. É uma das linguagens que o plano base já cogitava, e o protocolo fica tipado igual no backend e no frontend. Ele faz `spawn` do `adb`, sobe o servidor HTTP/WebSocket e roda o parser. **Python (asyncio + websockets) também serve**, porque o backend não decodifica nada. Escolha a linguagem que a equipe domina melhor.
- **Frontend: HTML + TypeScript + WebCodecs**. Decodifica por hardware, roda no Chrome e no Edge (que já vem no Windows) e é a **mesma UI para todas as fases**.
- **Empacotamento** (opcional, quando for distribuir): Tauri ou Electron envolvendo o backend e a página. O núcleo não muda.

### Por que WebCodecs no lugar de PyAV/OpenCV
- Decodifica por hardware (D3D11/VAAPI/VideoToolbox) e entrega um `VideoFrame` direto para a GPU: CPU abaixo de 5 % em 1080p60.
- O próprio navegador cuida do vsync, da tela cheia e do redimensionamento.
- Ponto de atenção: o Firefox só ganhou WebCodecs recentemente. Suportar oficialmente Chrome e Edge.

---

## 3. Código

### 3.1 Estrutura do repositório
```
/server        backend Node/TS
  /sources     UsbSource, WifiAdbSource, AppSource, ScreenrecordSource (só para o spike)
  /protocol    parser H.264, formato dos pacotes, mensagens de controle
  /record      remux para MP4 (ffmpeg -c copy ou mp4box.js)
/web           frontend (decoder, renderização, UI, captura de input)
/android       app Kotlin (Fase 3)
/vendor        scrcpy-server.jar (versão fixada + NOTICE da licença)
/docs
```

### 3.2 Interface das fontes (substitui `start()` / `read_frame()`)
```ts
interface VideoSource {
  start(cfg: { maxSize: number; fps: number; bitrate: number; codec: 'h264' }): Promise<StreamInfo>;
  stop(): Promise<void>;
  packets(): AsyncIterable<EncodedPacket>;      // uma access unit por pacote
  requestKeyframe(): void;                      // depois de perda, reconexão ou decoder novo
  setBitrate?(bps: number): void;               // taxa adaptativa
  control?: ControlChannel;                     // ausente no AppSource
  on(ev: 'config' | 'disconnected' | 'error', cb: (...a: any[]) => void): void;
}
interface EncodedPacket { data: Uint8Array; ptsUs: bigint; keyframe: boolean; }
interface StreamInfo   { width: number; height: number; codecString: string; /* ex.: avc1.42E01F */ }
```
O evento `config` dispara quando chegam SPS/PPS novos, por exemplo numa rotação. A UI então reconfigura o `VideoDecoder`.

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
- **Saída:** vídeo aparecendo no navegador e latência medida (seção 7).

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
- Transporte: WebSocket binário sobre TLS com o certificado fixado pelo QR. Assim a criptografia funciona sem CA e sem alerta no navegador, porque o navegador só fala com `localhost`.
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
| R13 | Licenças | B | A | `scrcpy-server` é Apache-2.0 (manter NOTICE). Não usar código GPL (UxPlay, ffmpeg compilado com GPL) dentro do produto |
| R14 | **Produto:** o scrcpy já faz as Fases 1, 2 e 4 de graça | A | A* | *Só pesa se o objetivo for lançar um produto. Diferenciais: UI sem terminal, pareamento por QR, vários aparelhos e a Fase 3 sem ADB |

---

## 6. Custo e viabilidade

### 6.1 Custo
| Item | Valor |
|---|---|
| Infraestrutura | **R$ 0**. Tudo roda local, sem servidor nem nuvem |
| Bibliotecas | R$ 0 (adb, scrcpy-server, Node e WebCodecs são gratuitos ou abertos) |
| Aparelhos de teste (2 a 3 usados: Samsung, Xiaomi, Motorola) | ~R$ 1.500–2.500, uma vez (opcional se já tiver) |
| Google Play (Fase 3) | US$ 25, uma vez (ou distribuir o APK direto, de graça) |
| Assinatura de código no Windows (só se distribuir o instalador) | ~US$ 10/mês (Azure Trusted Signing) |
| Espelhar pela internet (fora do escopo) | Precisaria de relay TURN: ~US$ 0,20–0,30 por hora por usuário a 8 Mbps |

### 6.2 Esforço (1 desenvolvedor)
| Fase | Prazo | Acumulado |
|---|---|---|
| 0 Spike | 1–2 dias | ~0,5 semana |
| 1 USB | 2–3 semanas | ~3 semanas |
| 2 Wi-Fi ADB | 1 semana | ~4 semanas |
| 3 App próprio | 5–7 semanas | ~10 semanas |
| 4 Extras | 1–2 semanas | ~12 semanas |

### 6.3 Viabilidade por fase
| Fase | Técnica | Observação |
|---|---|---|
| 0, 1 e 2 | **Alta** | Caminho já provado (scrcpy). O risco está nos fabricantes, não na tecnologia |
| 3 | **Média** | Consentimento por sessão, foreground service e fabricantes que encerram processos |
| 4: controle no modo ADB | **Alta** | Vem do `scrcpy-server` |
| 4: controle no app próprio | **Baixa a média** | AccessibilityService com risco na Play Store. Fica fora do plano |

---

## 7. Medição e testes
- **Latência real:** cronômetro em milissegundos na tela do celular, filmando o celular e o monitor juntos com o celular a 240 fps; média de 30 amostras. Repetir em USB, 5 GHz e 2.4 GHz.
- **Overlay de debug na UI:** fps, bitrate, frames descartados, tamanho da fila e RTT.
- **Testes de longa duração:** 2 horas de sessão, acompanhando o uso de memória do backend e da aba.
- **Testes automáticos:** parser H.264 (inclusive fuzzing com dados corrompidos) e protocolo de controle, rodando no CI do GitHub Actions.

## 8. Decisões tomadas (definitivo)
1. Pipeline único com `VideoSource` intercambiável: **mantido** do plano base.
2. Fonte da Fase 1: **`scrcpy-server`**. O `screenrecord` fica só no spike.
3. UI e decodificação: **página web local + WebCodecs** em todas as fases, no lugar de PyAV/OpenCV.
4. Backend: **Node.js + TypeScript** (Python é uma alternativa aceitável).
5. Transporte da Fase 3: **WebSocket + TLS fixado por QR**. WebRTC fica descartado enquanto o uso for só em LAN.
6. Controle remoto e clipboard: **sobem para a Fase 1** no modo ADB. No app próprio, só visualização.
7. Fase 3 só começa depois de usar as Fases 1 e 2 no dia a dia.
