import { API, detectOS, detectArchitecture, parseRelease, selectDownload } from '../lib/releases.mjs';
const theme=document.querySelector<HTMLButtonElement>('.theme-toggle');
if(theme) { theme.hidden=false; theme.addEventListener('click',()=>{const next=document.documentElement.dataset.theme==='dark'?'light':'dark';document.documentElement.dataset.theme=next;try{localStorage.setItem('mm-client-theme',next);}catch{}}); }
for(const link of document.querySelectorAll<HTMLAnchorElement>('[data-language]')) link.addEventListener('click',()=>{try{localStorage.setItem('mm-client-language',link.dataset.language!);}catch{}if(link.dataset.language==='ru')link.href=new URL('/spk-mm-client/?lang=ru',location.origin).href;});
const tabs=[...document.querySelectorAll<HTMLButtonElement>('[role=tab]')];
const panels=[...document.querySelectorAll<HTMLElement>('[data-gallery-panel]')];
function select(at:number){tabs.forEach((t,i)=>{t.setAttribute('aria-selected',String(i===at));t.tabIndex=i===at?0:-1;});panels.forEach((p,i)=>p.hidden=i!==at);}
tabs.forEach((tab,i)=>{tab.addEventListener('click',()=>select(i));tab.addEventListener('keydown',e=>{const next=e.key==='ArrowRight'?(i+1)%tabs.length:e.key==='ArrowLeft'?(i+tabs.length-1)%tabs.length:e.key==='Home'?0:e.key==='End'?tabs.length-1:null;if(next!==null){e.preventDefault();select(next);tabs[next].focus();}});});
const detectedOS = detectOS(navigator.userAgent, navigator.maxTouchPoints);
const osNames: Record<string,string> = {linux:'Linux',windows:'Windows',darwin:'macOS'};
const downloadButtons = [...document.querySelectorAll<HTMLAnchorElement>('[data-download-label]')];
const updateDownloadButtons = (selectedOS: string, file?: {url: string; name: string; format: string} | null) => {
  for (const button of downloadButtons) {
    const labels = JSON.parse(button.dataset.downloadLabel!) as {download: string; downloadCompactFormat: string; downloadFormatFor: string; choose: string; chooseCompact: string; chooseFor: string};
    const format = file?.format.toUpperCase();
    const text = file ? labels.downloadFormatFor.replace('{format}', format!).replace('{os}', osNames[selectedOS]) : osNames[selectedOS] ? `${labels.chooseFor} ${osNames[selectedOS]}` : labels.choose;
    button.href = file?.url || '#downloads';
    button.querySelector<HTMLElement>('[data-download-text]')!.textContent = text;
    const compact = button.querySelector<HTMLElement>('.compact-download-text');
    if (compact) compact.textContent = file ? labels.downloadCompactFormat.replace('{format}', format!) : labels.chooseCompact;
    button.setAttribute('aria-label', text);
    button.title = file ? `${text}: ${file.name}` : text;
  }
};
updateDownloadButtons(detectedOS);
async function browserArchitecture(): Promise<string> {
  const uaData = (navigator as Navigator & {userAgentData?: {getHighEntropyValues: (hints: string[]) => Promise<{architecture?: string; bitness?: string}>}}).userAgentData;
  if (!uaData) return detectArchitecture(navigator.userAgent);
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    const hints = await Promise.race([
      uaData.getHighEntropyValues(['architecture','bitness']).catch(() => undefined),
      new Promise<undefined>(resolve => { timer = setTimeout(() => resolve(undefined), 300); }),
    ]);
    return detectArchitecture(navigator.userAgent, hints);
  } finally { clearTimeout(timer); }
}
const host = document.querySelector<HTMLElement>('[data-downloads]');
if (host) {
  const labels: Record<string, string> = JSON.parse(host.dataset.labels!);
  const status = host.querySelector<HTMLElement>('[data-release-status]')!;
  const controls = host.querySelector<HTMLElement>('[data-download-controls]')!;
  const packages = host.querySelector<HTMLElement>('[data-packages]')!;
  const os = host.querySelector<HTMLSelectElement>('select[name=os]')!;
  const arch = host.querySelector<HTMLSelectElement>('select[name=arch]')!;
  status.hidden = false;
  status.textContent = labels.loading;
  os.value = detectedOS;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 8000);
  void (async () => {
    try {
      const [response, architecture] = await Promise.all([fetch(API, { signal: controller.signal, headers: { Accept: 'application/vnd.github+json' } }), browserArchitecture()]);
      arch.value = detectedOS ? architecture : '';
      if (response.status === 404) { status.textContent = labels.none; return; }
      if (!response.ok) throw new Error('Release service unavailable');
      const release = parseRelease(await response.json());
      if (!release || !release.files.some(file => !file.browser)) { status.textContent = labels.none; return; }
      controls.hidden = false;
      packages.hidden = false;
      const render = () => {
        updateDownloadButtons(os.value, selectDownload(release, os.value, arch.value));
        const files = release.files.filter(file => !file.browser && (!os.value || file.os === os.value) && (!arch.value || file.arch === arch.value));
        status.textContent = files.length ? `${labels.release} ${release.version}` : labels.noMatch;
        packages.replaceChildren(...files.map(file => {
          const item = document.createElement('article'); item.className = 'package';
          const info = document.createElement('div'); info.className = 'package-info';
          const title = document.createElement('strong');
          title.textContent = `${({linux:'Linux',windows:'Windows',darwin:'macOS'} as Record<string,string>)[file.os]} · ${file.arch === 'amd64' ? 'x86-64' : 'ARM64'}`;
          const detail = document.createElement('small'); detail.textContent = `${labels.desktopMode} / ${file.format.toUpperCase()}${file.size ? ' / ' + (file.size / 1048576).toFixed(1) + ' MB' : ''}`;
          info.append(title, detail);
          const link = document.createElement('a'); link.href = file.url; link.textContent = labels.download; link.setAttribute('aria-label', `${labels.download} ${file.name}`);
          item.append(info, link);
          if (file.checksum) { const checksum = document.createElement('a'); checksum.href = file.checksum; checksum.textContent = 'SHA-256'; checksum.setAttribute('aria-label', `${labels.checksum} ${file.name}`); item.append(checksum); }
          return item;
        }));
      };
      os.addEventListener('change', render); arch.addEventListener('change', render); render();
    } catch { status.textContent = labels.unavailable; }
    finally { clearTimeout(timer); }
  })();
}
