import './style.css';

import {
  Connect, Disconnect, DiscoverWiFi, EnableWiFi, Environment, GetSettings,
  ListDevices, Pair, SaveSettings, StartDemo, StartMirror, StopMirror,
} from '../wailsjs/go/main/App';
import { adb, main } from '../wailsjs/go/models';
import { EventsOn, WindowUnfullscreen } from '../wailsjs/runtime/runtime';
import { Mirror } from './mirror';
import { decoderHelp, decoderSelfTest } from './selftest';

const $ = <T extends HTMLElement = HTMLElement>(id: string) => document.getElementById(id) as T;

let env: main.EnvInfo;
let mirror: Mirror | null = null;
let decoderOk = true;
let busy = false;

// ---- mensagens ----

function banner(text: string, kind: 'warn' | 'error' | 'ok' = 'warn') {
  const el = $('banner');
  el.textContent = text;
  el.className = 'banner ' + kind;
  el.hidden = text === '';
}

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));

// Explica cada estado do adb em português, com o que fazer.
function stateHint(d: adb.Device): string {
  switch (d.state) {
    case 'device': return '';
    case 'unauthorized': return 'Desbloqueie o celular e toque em "Permitir" na pergunta de depuração USB.';
    case 'offline': return 'Aparelho offline. Desconecte e conecte o cabo de novo.';
    case 'no permissions':
      return env.os === 'linux'
        ? 'Sem permissão no USB. Instale as regras udev: sudo apt install android-sdk-platform-tools-common (ou equivalente) e reconecte.'
        : 'Sem permissão para acessar o aparelho.';
    default: return d.state;
  }
}

// ---- lista de aparelhos ----

function renderDevices(devs: adb.Device[] | null) {
  devs = devs ?? [];
  const usb = devs.filter((d) => !d.wifi);
  const wifi = devs.filter((d) => d.wifi);
  fillList($('usb-list'), usb, 'Nenhum aparelho no USB.', (d, li) => {
    if (d.state === 'device') {
      li.append(button('Espelhar', () => startMirror(d.serial)));
      li.append(button('Ativar Wi-Fi', () => enableWiFi(d.serial), 'secondary'));
    }
  });
  fillList($('wifi-list'), wifi, 'Nenhum aparelho conectado pelo Wi-Fi.', (d, li) => {
    if (d.state === 'device') li.append(button('Espelhar', () => startMirror(d.serial)));
    li.append(button('Desconectar', () => run(() => Disconnect(d.serial)), 'secondary'));
  });
}

function fillList(ul: HTMLElement, devs: adb.Device[], empty: string, actions: (d: adb.Device, li: HTMLLIElement) => void) {
  ul.replaceChildren();
  if (devs.length === 0) {
    const li = document.createElement('li');
    li.className = 'empty';
    li.textContent = empty;
    ul.append(li);
    return;
  }
  for (const d of devs) {
    const li = document.createElement('li');
    const info = document.createElement('div');
    info.className = 'device-info';
    const name = document.createElement('div');
    name.className = 'device-name';
    name.textContent = d.model || d.serial;
    const serial = document.createElement('div');
    serial.className = 'device-serial';
    serial.textContent = d.serial;
    info.append(name, serial);
    const hint = stateHint(d);
    if (hint) {
      const h = document.createElement('div');
      h.className = 'device-hint';
      h.textContent = hint;
      info.append(h);
    }
    const badge = document.createElement('span');
    badge.className = 'badge ' + (d.state === 'device' ? 'ok' : 'warn');
    badge.textContent = d.state === 'device' ? 'pronto' : d.state;
    li.append(info, badge);
    actions(d, li);
    ul.append(li);
  }
}

function button(label: string, onClick: () => void, cls = '') {
  const b = document.createElement('button');
  b.textContent = label;
  if (cls) b.className = cls;
  b.addEventListener('click', onClick);
  return b;
}

// Executa uma ação do Go, travando os botões e mostrando o erro se houver.
async function run<T>(fn: () => Promise<T>, okMsg?: string): Promise<T | undefined> {
  if (busy) return undefined;
  busy = true;
  document.querySelectorAll('button').forEach((b) => (b.disabled = true));
  try {
    const r = await fn();
    if (okMsg) banner(okMsg, 'ok');
    return r;
  } catch (e) {
    banner(errText(e), 'error');
    return undefined;
  } finally {
    busy = false;
    document.querySelectorAll('button').forEach((b) => (b.disabled = false));
  }
}

// ---- Wi-Fi ----

async function enableWiFi(serial: string) {
  banner('Ligando a depuração sem fio…');
  const r = await run(() => EnableWiFi(serial));
  if (!r) return;
  if (r.connected) {
    banner(r.message, 'ok');
  } else if (r.needsPairing) {
    banner(r.message);
    const box = $<HTMLDetailsElement>('pair-box');
    box.open = true;
    $<HTMLInputElement>('pair-addr').value = r.ip + ':';
    $<HTMLInputElement>('connect-addr').value = r.address;
    $<HTMLInputElement>('pair-addr').focus();
  }
}

// Depois de parear, conecta sozinho: o endereço de conexão (a porta é outra)
// vem do "Ativar Wi-Fi" ou do anúncio do celular na rede.
async function connectAfterPair(pairAddr: string) {
  const host = pairAddr.slice(0, pairAddr.lastIndexOf(':'));
  const input = $<HTMLInputElement>('connect-addr');
  let addr = input.value.trim();
  if (!addr.startsWith(host + ':')) {
    const svcs = (await run(() => DiscoverWiFi())) ?? [];
    addr = svcs.find((s) => s.type === 'connect' && s.address.startsWith(host + ':'))?.address ?? '';
  }
  if (!addr) {
    banner('Pareado, mas o celular ainda não apareceu na rede. Clique em Procurar para conectar.');
    return;
  }
  input.value = addr;
  await run(() => Connect(addr), `Pareado e conectado a ${addr}. Já pode tirar o cabo.`);
}

async function discover() {
  $('discover-status').textContent = 'Procurando…';
  const svcs = await run(() => DiscoverWiFi());
  const ul = $('mdns-list');
  ul.replaceChildren();
  $('discover-status').textContent = svcs ? `${svcs.length} encontrado(s)` : '';
  for (const s of svcs ?? []) {
    const li = document.createElement('li');
    const info = document.createElement('div');
    info.className = 'device-info';
    info.innerHTML = `<div class="device-name"></div><div class="device-serial"></div>`;
    info.children[0].textContent = s.type === 'connect' ? 'Pronto para conectar' : 'Aguardando pareamento';
    info.children[1].textContent = `${s.name} · ${s.address}`;
    li.append(info);
    if (s.type === 'connect') {
      li.append(button('Conectar', () => run(() => Connect(s.address), `Conectado a ${s.address}.`)));
    } else {
      li.append(button('Parear', () => {
        $<HTMLDetailsElement>('pair-box').open = true;
        $<HTMLInputElement>('pair-addr').value = s.address;
        $<HTMLInputElement>('pair-code').focus();
      }));
    }
    ul.append(li);
  }
}

// ---- espelhamento ----

async function startMirror(serial: string) {
  if (!decoderOk) {
    banner(decoderHelp(env.os), 'error');
    return;
  }
  banner(serial === 'demo' ? 'Iniciando a fonte de teste…' : 'Iniciando o espelhamento (na primeira vez o scrcpy-server é baixado)…');
  const info = await run(() => (serial === 'demo' ? StartDemo() : StartMirror(serial)));
  if (!info) return;
  banner('');
  showMirror(info);
}

function showMirror(info: main.MirrorInfo) {
  $('connect-view').hidden = true;
  $('mirror-view').hidden = false;
  mirror = new Mirror($('screen') as HTMLCanvasElement, $('status'), $('toolbar'), $('stats'), {
    url: info.url,
    deviceName: info.deviceName,
    control: info.control,
    audio: $<HTMLInputElement>('set-audio').checked,
    os: env.os,
    debug: env.debug,
    onEnd: (reason) => leaveMirror(reason),
  });
  mirror.start();
}

function leaveMirror(reason: string) {
  if (!mirror) return;
  mirror.stop();
  mirror = null;
  StopMirror();
  WindowUnfullscreen();
  $('mirror-view').hidden = true;
  $('connect-view').hidden = false;
  if (reason) banner('O espelhamento terminou: ' + reason, 'error');
  ListDevices().then(renderDevices).catch(() => {});
}

// ---- configurações e diagnóstico ----

async function loadSettings() {
  const s = await GetSettings();
  $<HTMLSelectElement>('set-size').value = String(s.maxSize);
  $<HTMLSelectElement>('set-fps').value = String(s.maxFps);
  $<HTMLSelectElement>('set-bitrate').value = String(s.bitRateMbps);
  $<HTMLInputElement>('set-control').checked = s.control;
  $<HTMLInputElement>('set-awake').checked = s.stayAwake;
  $<HTMLInputElement>('set-audio').checked = s.audio;
  $('settings-form').addEventListener('change', () => {
    SaveSettings(main.Settings.createFrom({
      maxSize: Number($<HTMLSelectElement>('set-size').value),
      maxFps: Number($<HTMLSelectElement>('set-fps').value),
      bitRateMbps: Number($<HTMLSelectElement>('set-bitrate').value),
      control: $<HTMLInputElement>('set-control').checked,
      stayAwake: $<HTMLInputElement>('set-awake').checked,
      audio: $<HTMLInputElement>('set-audio').checked,
    })).catch((e) => banner(errText(e), 'error'));
  });
}

function renderDiag(decoder: string) {
  const rows: [string, string][] = [
    ['Sistema', env.os],
    ['adb', env.adbError || `${env.adbVersion} (${env.adbPath})`],
    ['scrcpy-server', 'v' + env.serverVersion],
    ['Decodificador H.264', decoder],
    ['Pasta de dados', env.dataDir],
  ];
  const dl = $('diag');
  dl.replaceChildren();
  for (const [k, v] of rows) {
    const dt = document.createElement('dt');
    dt.textContent = k;
    const dd = document.createElement('dd');
    dd.textContent = v;
    dl.append(dt, dd);
  }
}

// ---- início ----

async function init() {
  env = await Environment();
  $('env-line').textContent = `${env.os} · scrcpy-server v${env.serverVersion}${env.debug ? ' · debug' : ''}`;
  renderDiag('testando…');
  if (env.adbError) banner(env.adbError, 'error');

  const test = await decoderSelfTest();
  decoderOk = test.ok;
  renderDiag(test.ok ? 'ok' : 'FALHOU: ' + test.detail);
  if (!test.ok) banner(decoderHelp(env.os), 'error');

  await loadSettings();
  EventsOn('devices', renderDevices);
  EventsOn('mirror:end', (reason: string) => leaveMirror(reason || ''));
  if (!env.adbError) ListDevices().then(renderDevices).catch((e) => banner(errText(e), 'error'));
  else renderDevices([]);

  $('discover-btn').addEventListener('click', discover);
  $<HTMLButtonElement>('demo-btn').disabled = !env.demoAvailable;
  $('demo-btn').addEventListener('click', () => startMirror('demo'));
  $('pair-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const addr = $<HTMLInputElement>('pair-addr').value.trim();
    const code = $<HTMLInputElement>('pair-code').value.trim();
    const ok = await run(() => Pair(addr, code).then(() => true));
    if (!ok) return;
    $<HTMLInputElement>('pair-code').value = '';
    await connectAfterPair(addr);
  });
  $('connect-form').addEventListener('submit', (e) => {
    e.preventDefault();
    const addr = $<HTMLInputElement>('connect-addr').value.trim();
    run(() => Connect(addr), `Conectado a ${addr}.`);
  });

  if (env.autostart) startMirror(env.autostart);
}

init().catch((e) => banner(errText(e), 'error'));
