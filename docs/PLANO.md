# Plano definitivo — screensharer

> Espelhamento da tela do celular no PC, com baixa latência, controle por mouse/teclado e custo de infraestrutura zero.

## 0. Contexto e premissas

O repositório tinha só um `README.md` vazio e nenhuma issue ou PR, então o plano anterior não foi encontrado. Este documento parte das premissas abaixo. Se alguma estiver errada, veja a seção 11 ("Decisões em aberto") e ajuste.

| Premissa | Valor assumido |
|---|---|
| Origem da tela | Celular **Android** (iOS fica para depois, ver 6.3) |
| Destino | PC **Windows** (prioridade), depois Linux e macOS |
| Rede | USB e Wi-Fi local (LAN). **Sem** espelhamento pela internet no MVP |
| Uso | Pessoal ou produtividade (demonstração, gravação, controlar o celular pelo PC) |
| Equipe | 1 a 2 desenvolvedores |

---

## 1. Decisão de arquitetura (resumo)

**MVP = modelo "scrcpy": um servidor que roda via ADB no celular e um cliente nativo no PC.**
**Fase 2 = app Android com MediaProjection, para quem não quer ativar a depuração USB.**

```
┌──────────── Android ────────────┐        USB (adb forward) ou Wi-Fi (TCP/TLS)       ┌────────────── PC ──────────────┐
│ captura de tela (Surface)       │                                                    │ demux → decodificador por HW   │
│   → MediaCodec H.264/H.265 (HW) │ ── vídeo ──────────────────────────────────────▶  │   (D3D11VA / VAAPI / VideoTB)  │
│ captura de áudio → Opus         │ ── áudio ──────────────────────────────────────▶  │ → renderização (GPU) + áudio   │
│ injeção de input / clipboard    │ ◀─ controle (mouse, teclado, clipboard) ────────  │ captura de input / UI          │
└─────────────────────────────────┘                                                    └────────────────────────────────┘
```

### Por que essa abordagem

| Critério | A) Servidor via ADB (escolhida p/ MVP) | B) App com MediaProjection (Fase 2) | C) WebRTC no navegador |
|---|---|---|---|
| Latência | **35–70 ms (USB)** | 60–120 ms | 100–200 ms |
| Controle do celular pelo PC | Sim, nativo (InputManager no shell) | Só via AccessibilityService (risco de política da Play Store) | Igual a B |
| Instalação no celular | Nenhuma (servidor enviado pelo adb) | Instala um app | Instala um app |
| Fricção para o usuário | Ativar a depuração USB | Consentimento a cada sessão (obrigatório no Android 14+) | Igual a B |
| Risco técnico | Baixo (scrcpy prova que funciona) | Médio | Médio |
| Tempo até o MVP | **4–6 semanas** | +6–8 semanas | 6–8 semanas |

**Conclusão:** A entrega o melhor desempenho no menor prazo. B resolve a fricção para quem não é técnico e entra depois. C só se justifica se o requisito for "abrir no navegador, sem instalar nada no PC".

---

## 2. Stack

| Camada | Escolha | Motivo | Alternativa |
|---|---|---|---|
| Servidor Android | Java/Kotlin, roda com `app_process` como usuário shell | Acesso a SurfaceControl/DisplayManager e injeção de input sem root | — |
| Base do servidor | **Fork do `scrcpy-server` (Apache-2.0)** no MVP | Evita reescrever meses de compatibilidade entre versões e fabricantes | Servidor próprio do zero (+2 a 3 meses) |
| Cliente PC (núcleo) | **Rust** + FFmpeg (`libavcodec`, link dinâmico LGPL) | Desempenho, segurança de memória, multiplataforma | C++ |
| Renderização | SDL2 ou `wgpu` | Apresentação na GPU, vsync controlável | — |
| Interface | **Tauri** (configurações, lista de dispositivos) com o vídeo em janela nativa | Instalador de ~10 MB contra ~150 MB do Electron | Electron + WebCodecs (desenvolvimento mais rápido, uso de RAM 3 a 4x maior) |
| Codec | H.264 Baseline/Main por padrão, H.265 opcional, AV1 quando o hardware tiver | O H.264 tem encoder por hardware em praticamente todos os aparelhos | — |
| Áudio | Opus, 48 kHz (Android 11+) | Baixa latência e pouca banda | AAC |

---

## 3. Desempenho: metas e orçamento

### 3.1 Metas (critério de aceite)

| Métrica | USB | Wi-Fi 5 GHz | Wi-Fi 2.4 GHz |
|---|---|---|---|
| Latência ponta a ponta (p50) | **≤ 70 ms** | ≤ 110 ms | ≤ 180 ms (modo econômico) |
| Latência p95 | ≤ 100 ms | ≤ 160 ms | ≤ 250 ms |
| FPS | 60 | 60 | 30 |
| CPU no PC (com decodificação por HW) | < 8 % de um núcleo | idem | idem |
| RAM no PC | < 150 MB | idem | idem |
| Tempo até o primeiro frame | < 1,5 s | < 3 s | < 3 s |
| Bateria do celular | < 15 %/h a 1080p60 | idem | idem |

### 3.2 Orçamento de latência (USB, 1080p60)

| Etapa | ms |
|---|---|
| Composição da tela + captura | 8–16 |
| Codificação por HW | 5–15 |
| Transporte USB / adb | 1–5 |
| Decodificação por HW | 3–8 |
| Renderização + vsync do monitor | 8–16 |
| **Total** | **~35–60** |

### 3.3 Técnicas obrigatórias
- **Encoder:** `KEY_LOW_LATENCY` (Android 11+), `KEY_PRIORITY=0`, sem B-frames, `REPEAT_PREVIOUS_FRAME_AFTER` para telas paradas, intervalo de I-frame longo (10 s ou mais) com **pedido de keyframe sob demanda** quando houver perda ou reconexão.
- **Sem buffer de jitter no USB.** No Wi-Fi, buffer adaptativo de 0 a 50 ms.
- **Descartar em vez de enfileirar:** se o decodificador ou a renderização atrasar, descarta frames antigos e mostra o mais recente. Atraso acumulado é o pior defeito possível nesse tipo de produto.
- **Bitrate e resolução adaptativos:** começa em 8 Mbps (1080p60) e cai para 4 Mbps, 720p ou 30 fps conforme o RTT e a fila de envio crescem. Isso também ajuda quando o celular esquenta (thermal throttling).
- **Caminho de dados sem cópias** no PC: frame decodificado direto para textura da GPU (D3D11, sem passar pela CPU).
- Resolução alinhada a múltiplos de 16, porque vários encoders (Samsung, MediaTek) falham com tamanhos ímpares.

### 3.4 Banda

| Perfil | Banda | Por hora |
|---|---|---|
| 1080p60 H.264 | 8 Mbps | ~3,6 GB |
| 1080p60 H.265 | 5 Mbps | ~2,2 GB |
| 720p30 | 2,5 Mbps | ~1,1 GB |
| Áudio Opus | 128 kbps | ~58 MB |

Em LAN isso não custa nada. O custo só aparece se um dia houver relay pela internet (seção 5).

---

## 4. Riscos

Probabilidade (P) e impacto (I): A = alto, M = médio, B = baixo.

| # | Risco | P | I | Mitigação |
|---|---|---|---|---|
| R1 | **Concorrência com o scrcpy, que é gratuito e maduro.** O produto pode não ter motivo para existir | A | A | Diferenciar pela experiência de uso: sem linha de comando, descoberta automática, pareamento Wi-Fi por QR code, vários aparelhos ao mesmo tempo, gravação com um clique. Validar com 5 a 10 usuários antes da Fase 2 |
| R2 | Mudança de APIs ocultas a cada versão do Android (14, 15, 16…) quebra a captura ou o input | A | A | Acompanhar o upstream do scrcpy; matriz de testes por versão; caminhos alternativos por versão no código |
| R3 | Fragmentação de encoders (falhas em resoluções, travamentos em Xiaomi, Samsung, MediaTek) | A | M | Lista de encoders alternativos, tentar de novo com resolução menor, flag `--encoder`, base de quirks por modelo |
| R4 | Fabricantes com restrições extras (a MIUI exige "Depuração USB (configurações de segurança)" para input) | M | M | Assistente de configuração que detecta o fabricante e mostra o passo a passo |
| R5 | Apps com `FLAG_SECURE` (bancos, Netflix) aparecem pretos | A | B | **Não contornar** (seria ilegal e contra os termos de uso). Avisar o usuário na interface |
| R6 | Instabilidade do Wi-Fi (jitter, 2.4 GHz congestionado) | A | M | Taxa adaptativa, recomendar 5 GHz, indicador de qualidade na tela, USB como modo recomendado |
| R7 | Segurança: `adb tcpip 5555` deixa o celular exposto na rede sem autenticação | M | **A** | **Proibido no produto.** Usar só a depuração sem fio do Android 11+ (pareamento com TLS). Na Fase 2: TLS 1.3 + PIN/QR, escutar só na LAN |
| R8 | Superaquecimento e bateria em sessões longas | M | M | Cair automaticamente para 30 fps com tela parada, limite térmico, modo econômico |
| R9 | Fase 2: a Play Store recusa o uso de AccessibilityService para input | M | M | Lançar a Fase 2 só com visualização; controle continua pelo modo ADB; distribuir APK fora da loja, se for preciso |
| R10 | Licenças (GPL contaminando o código) | B | A | FFmpeg com link dinâmico e sem `--enable-gpl`; não usar x264 nem UxPlay (GPL-3) no núcleo; scrcpy é Apache-2.0 (manter NOTICE) |
| R11 | SmartScreen do Windows bloqueia instalador sem assinatura | A | M | Assinar o binário (seção 5) |
| R12 | Escopo crescer (iOS, internet, multiusuário) antes de o MVP estar estável | M | A | Roadmap rígido por fases com critério de saída (seção 7) |

---

## 5. Custos

### 5.1 Infraestrutura (MVP só em LAN)
**R$ 0/mês.** Não há servidor: tudo é ponto a ponto.

Se um dia houver espelhamento pela internet, vai precisar de TURN/relay. Com 8 Mbps são cerca de 3,6 GB por hora de sessão; a US$ 0,05–0,09/GB, isso dá **~US$ 0,20–0,30 por hora por usuário** que passa pelo relay. Por isso esse recurso fica fora do plano até existir um modelo de receita.

### 5.2 Custos fixos e de distribuição

| Item | Custo | Quando |
|---|---|---|
| Domínio + site estático (GitHub Pages) | ~US$ 12/ano | MVP |
| Assinatura de código Windows (Azure Trusted Signing) | ~US$ 10/mês | Release pública |
| Conta Google Play | US$ 25 (uma vez) | Fase 2 |
| Apple Developer (macOS notarizado / iOS) | US$ 99/ano | Fase 3 |
| Aparelhos de teste (3 a 5 Android usados de fabricantes diferentes) | ~R$ 2.000–3.500 (uma vez) | MVP |
| CI (GitHub Actions) | grátis em repositório público; ~US$ 0–20/mês em privado | Contínuo |

### 5.3 Esforço (1 desenvolvedor)

| Fase | Semanas |
|---|---|
| MVP (Android via USB e Wi-Fi para Windows) | 4–6 |
| Polimento + Linux/macOS | 3–4 |
| Fase 2 (app MediaProjection) | 6–8 |
| Fase 3 (iOS) | 6–10 (incerto) |

---

## 6. Viabilidade

### 6.1 Técnica: **alta**
O caminho via ADB já foi comprovado pelo scrcpy, com milhões de usuários. Os encoders por hardware estão em praticamente todo Android desde a versão 5.

### 6.2 De produto: **média**, e esse é o principal risco (R1)
Só vale construir se a experiência for claramente melhor que a do scrcpy, ou se o objetivo for aprendizado ou portfólio. Nesse último caso, o plano continua válido e a seção de mercado pode ser ignorada.

### 6.3 iOS: **baixa a média**, fora do MVP
- Um receptor AirPlay no PC funciona, mas o protocolo não tem documentação oficial e as implementações abertas (UxPlay) são GPL-3.
- Um app com ReplayKit Broadcast Extension tem limite de ~50 MB de memória na extensão e não permite controlar o aparelho.
- Não existe equivalente ao ADB para injetar input no iOS.

---

## 7. Roadmap com critérios de saída

### Fase 0: Prova de conceito (1 semana)
- Fork do scrcpy-server e cliente Rust mínimo que recebe H.264 pelo `adb forward` e decodifica com FFmpeg por hardware.
- **Saída:** vídeo a 1080p60 com latência ≤ 80 ms medida (ver seção 8).

### Fase 1: MVP (4–6 semanas)
1. Detecção automática de aparelhos (USB), envio e inicialização do servidor
2. Vídeo + controle (mouse, teclado, rolagem, gestos básicos) + clipboard
3. Wi-Fi via pareamento de depuração sem fio do Android 11+ (QR code na interface)
4. Taxa adaptativa e descarte de frames
5. Áudio (Android 11+)
6. Interface Tauri: lista de aparelhos, qualidade, tela cheia, rotação
7. Instalador assinado para Windows e atualização automática
- **Saída:** metas da seção 3.1 atingidas em 5 aparelhos (Samsung, Xiaomi, Motorola, Pixel e um MediaTek) nas versões Android 10 a 16; sessão de 2 horas sem queda nem atraso acumulado.

### Fase 2: Sem ADB (6–8 semanas), só se a Fase 1 for validada com usuários
- App Android com MediaProjection + serviço em primeiro plano, descoberta por mDNS, TLS 1.3 com pareamento por PIN/QR.
- Somente visualização (o controle continua no modo ADB).

### Fase 3: Expansão (opcional)
- Linux/macOS, gravação MP4, vários aparelhos lado a lado, iOS (pesquisa de viabilidade primeiro).

---

## 8. Plano de testes e medição
- **Latência real:** cronômetro em milissegundos no celular, filmando o celular e o monitor juntos a 240 fps; diferença média de 30 amostras.
- **Telemetria local:** timestamp de captura no pacote → cálculo da latência no PC, mostrado num overlay de debug (fps, bitrate, frames descartados, RTT).
- **Matriz de aparelhos:** 5 aparelhos × (USB, 5 GHz, 2.4 GHz) × (1080p60, 720p30).
- **Testes de longa duração:** 2 horas, monitorando a memória (sem vazamento) e a temperatura do aparelho.
- **CI:** build e testes unitários do protocolo (parser de pacotes, fuzzing do demux) a cada PR.

## 9. Segurança e privacidade
- Nada sai da rede local; não há telemetria remota sem opt-in.
- Proibido usar ADB por TCP sem autenticação; só pareamento com TLS.
- O parser do protocolo no PC precisa de fuzzing, porque recebe dados vindos da rede.
- Respeitar `FLAG_SECURE`; nunca tentar contornar.

## 10. Licenciamento
- Código próprio: manter a `LICENSE` atual do repositório (é compatível com Apache-2.0).
- scrcpy-server: Apache-2.0, manter os avisos de copyright e o arquivo NOTICE.
- FFmpeg: LGPL com link dinâmico, sem componentes GPL.
- SDL2 (zlib) e Tauri (MIT/Apache): sem restrições.

## 11. Decisões em aberto (para o dono do projeto)
1. **Origem e destino estão certos?** (Android → PC). Se for PC → TV/navegador ou PC → PC, a arquitetura muda para WebRTC/captura de desktop (DXGI Desktop Duplication).
2. **Objetivo:** produto para o público ou projeto pessoal/portfólio? Isso muda o peso do risco R1 e do custo de assinatura de código.
3. **Fork do scrcpy-server ou servidor próprio?** A recomendação é o fork no MVP.
4. **Rust + Tauri ou Electron?** A recomendação é Rust + Tauri, a menos que a equipe só domine JavaScript.
