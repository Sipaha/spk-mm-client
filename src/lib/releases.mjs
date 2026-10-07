export const REPO='https://github.com/Sipaha/spk-mm-client';
export const API='https://api.github.com/repos/Sipaha/spk-mm-client/releases/latest';
export function parseRelease(value) {
  if (!value || value.draft || value.prerelease || typeof value.tag_name!=='string' || !Array.isArray(value.assets)) return null;
  const valid=a=>{try {const u=new URL(a.browser_download_url);return u.origin==='https://github.com'&&u.pathname.startsWith('/Sipaha/spk-mm-client/releases/download/')&&!u.username&&!u.password;}catch{return false;}};
  const assets=value.assets.filter(a=>a&&typeof a.name==='string'&&valid(a));
  return {version:value.tag_name,files:assets.filter(a=>/^spk-mm-client[^/]*\.(exe|msi|dmg|pkg|deb|rpm|zip|tar\.gz)$/.test(a.name)&&!a.name.startsWith('spk-mm-client-browser')).map(a=>({...a,checksum:assets.find(x=>x.name===a.name+'.sha256')?.browser_download_url}))};
}
