#!/usr/bin/env python3
"""Start only the packaged production client, with a fresh isolated CI profile."""
import argparse,json,os,re,subprocess,time,tempfile,shutil
from pathlib import Path
from release import ROOT
from diagnostics import report_failure

def smoke(platform,arch,release_version):
    scratch=Path(os.environ['MM_RELEASE_SCRATCH'])/'native-smoke';scratch.mkdir(parents=True,exist_ok=True)
    profile=Path(tempfile.mkdtemp(prefix='native-',dir=scratch.parent));env=dict(os.environ)
    for key,name in {'HOME':'home','USERPROFILE':'home','SPK_MM_CLIENT_HOME':'data','SPK_MM_CLIENT_DOWNLOADS':'downloads','XDG_CONFIG_HOME':'config','XDG_DATA_HOME':'share','XDG_CACHE_HOME':'cache','XDG_RUNTIME_DIR':'run','APPDATA':'appdata','LOCALAPPDATA':'localappdata'}.items():
        path=profile/name;path.mkdir(exist_ok=True);env[key]=str(path)
    env.update(DBUS_SESSION_BUS_ADDRESS='unix:path='+str(profile/'no-bus'),LANG='en_US.UTF-8',LANGUAGE='en',SPK_MM_CLIENT_GPU='never')
    for key in ('HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy'):env.pop(key,None)
    stage=ROOT/'build'/f'package-{platform}-{arch}'
    binary=ROOT/'build/bin/spk-mm-client-release' if platform=='linux' else stage/('spk-mm-client.exe' if platform=='windows' else 'SPK MM Client.app/Contents/MacOS/spk-mm-client')
    assert subprocess.check_output([str(binary),'version'],env=env,text=True).strip()=='spk-mm-client '+release_version
    refused=subprocess.run([str(binary),'--mm-fake'],env=env,capture_output=True,text=True,timeout=30)
    if refused.returncode==0:raise ValueError('Production desktop accepted development fake flags')
    png=scratch/f'{platform}-{arch}.png'
    with (scratch/'app.log').open('wb') as log:
        child=subprocess.Popen([str(binary)],env=env,stdout=log,stderr=log)
        try:
            deadline=time.monotonic()+100;window=None
            while time.monotonic()<deadline:
                if child.poll() is not None:raise RuntimeError('Production app exited during startup')
                if platform=='linux':
                    tree=subprocess.check_output(['xwininfo','-root','-tree'],env=env,text=True)
                    for line in tree.splitlines():
                        match=re.search(r'(0x[0-9a-f]+).*"spk-mm-client"',line)
                        if match and subprocess.check_output(['xprop','-id',match[1],'_NET_WM_PID'],env=env,text=True).strip().endswith('= '+str(child.pid)):
                            window=match[1];break
                    if window:
                        subprocess.run(['import','-window',window,str(png)],env=env,check=True)
                        text=subprocess.check_output(['tesseract',str(png),'stdout','--psm','6'],env=env,text=True,stderr=subprocess.DEVNULL)
                        normalized=re.sub('[^a-z0-9]','',text.lower())
                        if 'spkmmclient' in normalized and 'addamattermostserver' in normalized:break
                elif platform=='windows':
                    result=subprocess.run(['powershell.exe','-NoProfile','-NonInteractive','-File',str(ROOT/'packaging/windows/smoke-ui.ps1'),'-ProcessId',str(child.pid),'-Version',release_version,'-OutputPath',str(png)],env=env,capture_output=True,text=True,timeout=120)
                    if result.returncode:raise RuntimeError(result.stdout+'\n'+result.stderr)
                    print(result.stdout);break
                else:
                    result=subprocess.run([str(Path(os.environ['GOBIN'])/'native-macos-smoke'),str(child.pid),str(png)],env=env,capture_output=True,text=True,timeout=60)
                    if result.returncode==0:print(result.stdout);break
                time.sleep(1)
            else:raise RuntimeError('Production webview did not render the actual application')
            if not png.exists() or png.stat().st_size<4096:raise RuntimeError('Native screenshot is empty')
            report={'platform':platform,'arch':arch,'version':release_version,'pid':child.pid,'window':window,'production':True,'fakeFlagsRefused':True,'screenshot':png.name,'commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip()}
            (scratch/'report.json').write_text(json.dumps(report,indent=2));print('PASS native production window, isolated profile, actual CLI version and screenshot',platform,arch)
        except Exception as error:
            # Preserve the actual UI failure even if later profile cleanup fails.
            report_failure(error)
            raise
        finally:
            if child.poll() is None:
                if platform=='windows':
                    # This live Popen PID belongs to our isolated test. WebView2
                    # children can keep the profile locked after parent-only kill.
                    subprocess.run(['taskkill.exe','/PID',str(child.pid),'/T','/F'],capture_output=True,check=True)
                else:child.terminate()
                child.wait(timeout=20)
            deadline=time.monotonic()+20
            while True:
                try:shutil.rmtree(profile);break
                except OSError:
                    if time.monotonic()>=deadline:raise
                    time.sleep(.5)
if __name__=='__main__':
    parser=argparse.ArgumentParser();parser.add_argument('--os',choices=['linux','windows','darwin'],required=True);parser.add_argument('--arch',choices=['amd64','arm64'],required=True);parser.add_argument('--version',required=True);args=parser.parse_args()
    try:smoke(args.os,args.arch,args.version)
    except Exception as error:
        report_failure(error)
        raise
