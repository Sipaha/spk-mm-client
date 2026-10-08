import test from 'node:test';import assert from 'node:assert/strict';import {parseRelease,REPO} from './releases.mjs';
const asset=(name,url=REPO+'/releases/download/v1.0.0/'+name)=>({name,browser_download_url:url,size:123});
test('only actual project files and matching sidecars are linked',()=>{const d=parseRelease({tag_name:'v1.0.0',assets:[asset('spk-mm-client_1.0.0_linux_amd64.deb'),asset('spk-mm-client_1.0.0_linux_amd64.deb.sha256'),asset('spk-mm-client-browser_1.0.0.zip'),asset('spk-mm-client.exe','https://foreign.example/spk-mm-client.exe')]});assert.equal(d.files.length,1);assert(d.files[0].checksum.endsWith('.sha256'));});
test('draft/prerelease/invalid data do not claim published packages',()=>{for(const d of [null,{}, {draft:true,tag_name:'v1',assets:[]},{prerelease:true,tag_name:'v1',assets:[]}])assert.equal(parseRelease(d),null);});

import { detectOS, detectArchitecture, selectDownload } from './releases.mjs';
const stable = names => ({tag_name:'v1.0.0',assets:names.map(name=>asset(name))});
test('selects native installers for all six OS/architecture combinations',()=>{
 const names=[];
 for(const os of ['linux','windows','darwin'])for(const arch of ['amd64','arm64']){
  const format={linux:'deb',windows:'msi',darwin:'dmg'}[os];
  names.push(`spk-mm-client_1.0.0_${os}_${arch}.${format}`,`spk-mm-client-browser_1.0.0_${os}_${arch}.${os==='windows'?'zip':'tar.gz'}`);
 }
 const release=parseRelease(stable(names));assert.equal(release.files.length,6);
 for(const os of ['linux','windows','darwin'])for(const arch of ['amd64','arm64']){
  const selected=selectDownload(release,os,arch);
  assert.equal(selected.os,os);assert.equal(selected.arch,arch);
  assert.equal(selected.format,{linux:'deb',windows:'msi',darwin:'dmg'}[os]);
  assert(!selected.name.includes('-browser_'));
 }
});
test('asset order cannot promote an archive above an installer',()=>{
 const names=['spk-mm-client_1.0.0_linux_amd64.tar.gz','spk-mm-client_1.0.0_linux_amd64.rpm','spk-mm-client_1.0.0_linux_amd64.deb'];
 for(const order of [names,[...names].reverse()])assert.equal(selectDownload(parseRelease(stable(order)),'linux','amd64').format,'deb');
 assert.equal(selectDownload(parseRelease(stable(names.slice(0,2))),'linux','amd64').format,'rpm');
 assert.equal(selectDownload(parseRelease(stable(names.slice(0,1))),'linux','amd64').format,'tar.gz');
});
test('stale versions and cross-platform file formats never become downloads',()=>{
 const data=stable(['spk-mm-client_1.0.0_linux_arm64.deb','spk-mm-client_0.9.0_linux_arm64.deb','spk-mm-client_1.0.0_linux_arm64.msi','spk-mm-client_1.0.0_windows_arm64.dmg','spk-mm-client_1.0.0_windows_arm64.exe']);
 assert.deepEqual(parseRelease(data).files.map(file=>file.name),['spk-mm-client_1.0.0_linux_arm64.deb']);
 for(const [os,arch] of [['','amd64'],['darwin',''],['windows','amd64']])assert.equal(selectDownload(parseRelease(data),os,arch),null);
});
test('desktop hints distinguish ARM64 and x86-64 without guessing Mac architecture',()=>{
 assert.equal(detectOS('Windows NT 10.0; Win64; x64'),'windows');
 assert.equal(detectArchitecture('Windows NT 10.0; Win64; x64'),'amd64');
 assert.equal(detectArchitecture('Windows NT 10.0; Win64; x64',{architecture:'arm',bitness:'64'}),'arm64');
 assert.equal(detectArchitecture('Linux x86_64'),'amd64');
 assert.equal(detectArchitecture('Linux aarch64'),'arm64');
 assert.equal(detectArchitecture('Macintosh; Intel Mac OS X'),'');
 assert.equal(detectArchitecture('Macintosh; Intel Mac OS X',{architecture:'arm',bitness:'64'}),'arm64');
 assert.equal(detectArchitecture('Macintosh; Intel Mac OS X',{architecture:'x86',bitness:'64'}),'amd64');
 assert.equal(detectArchitecture('Linux x86_64',{architecture:'x86',bitness:'32'}),'');
 assert.equal(detectOS('Linux; Android 15; Mobile'),'');
 assert.equal(detectOS('Macintosh; Intel Mac OS X',5),'');
 assert.equal(detectOS('X11; CrOS x86_64'),'');
});
