param([Parameter(Mandatory=$true)][string]$Version, [Parameter(Mandatory=$true)][string]$Arch)
$ErrorActionPreference = 'Stop'
$root = (Get-Location).Path
$msi = Join-Path $root "dist/$Version/windows-$Arch/spk-mm-client_${Version}_windows_${Arch}.msi"
$install = Join-Path $env:MM_RELEASE_SCRATCH 'msi-install'
$log = Join-Path $env:MM_RELEASE_SCRATCH 'msi-install.log'
$removeLog = Join-Path $env:MM_RELEASE_SCRATCH 'msi-remove.log'
# Inspect the real MSI identity and numeric version before installation.
$installer = New-Object -ComObject WindowsInstaller.Installer
$db = $installer.OpenDatabase($msi, 0)
$template = $db.SummaryInformation(0).Property(7)
$expectedPlatform = if ($Arch -eq 'amd64') { 'x64' } else { 'Arm64' }
if (($template -split ';')[0] -ne $expectedPlatform) { throw "MSI platform mismatch: $template" }
foreach ($entry in @{ProductName="SPK MM Client $Version";ProductVersion=($Version -split '-')[0];Manufacturer='Pavel Simonov';UpgradeCode='{7B480BA7-D90B-4FA1-91D9-79B89AD6542E}'}.GetEnumerator()) {
  $view = $db.OpenView("SELECT ``Value`` FROM ``Property`` WHERE ``Property``='$($entry.Key)'")
  $view.Execute()
  $record = $view.Fetch()
  if (!$record -or $record.StringData(1) -ne $entry.Value) { throw "MSI property mismatch: $($entry.Key)" }
  $view.Close()
}
[void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($db)
[void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($installer)
$p = Start-Process msiexec.exe -ArgumentList "/i `"$msi`" /qn /norestart INSTALLFOLDER=`"$install`" /l*v `"$log`"" -Wait -PassThru
if ($p.ExitCode -notin @(0,3010)) { throw "MSI install failed: $($p.ExitCode)" }
try {
  $source = Join-Path $root "build/package-windows-$Arch/spk-mm-client.exe"
  $installed = Join-Path $install 'spk-mm-client.exe'
  if ((Get-FileHash $source).Hash -ne (Get-FileHash $installed).Hash) { throw 'MSI executable content mismatch' }
  if ((Get-FileHash (Join-Path $root 'LICENSE')).Hash -ne (Get-FileHash (Join-Path $install 'LICENSE')).Hash) { throw 'MSI license mismatch' }
  if ((Get-FileHash (Join-Path $root 'THIRD-PARTY-NOTICES.txt')).Hash -ne (Get-FileHash (Join-Path $install 'THIRD-PARTY-NOTICES.txt')).Hash) { throw 'MSI third-party notices mismatch' }
  if ((Get-FileHash (Join-Path $root 'NOTICE')).Hash -ne (Get-FileHash (Join-Path $install 'NOTICE')).Hash) { throw 'MSI NOTICE mismatch' }
  $handler=(Get-ItemProperty 'HKCU:\Software\Classes\mmauth\shell\open\command').'(default)'
  if ($handler -notmatch [regex]::Escape($installed)) { throw 'MSI mmauth handler mismatch' }
  $shortcut = Join-Path ([Environment]::GetFolderPath('Programs')) 'SPK MM Client.lnk'
  if (!(Test-Path $shortcut)) { throw 'Start menu shortcut missing' }
} finally {
  $p = Start-Process msiexec.exe -ArgumentList "/x `"$msi`" /qn /norestart /l*v `"$removeLog`"" -Wait -PassThru
  if ($p.ExitCode -notin @(0,3010)) { throw "MSI removal failed: $($p.ExitCode)" }
}
if (Test-Path (Join-Path $install 'spk-mm-client.exe')) { throw 'MSI executable was not removed' }
if (Test-Path (Join-Path $install 'THIRD-PARTY-NOTICES.txt')) { throw 'MSI third-party notices were not removed' }
Write-Host 'Verified MSI metadata, real install, payload, shortcut and removal'
