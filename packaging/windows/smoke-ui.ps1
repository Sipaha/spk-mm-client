param([int]$ProcessId,[string]$Version,[string]$OutputPath)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
$deadline = [DateTime]::UtcNow.AddSeconds(90)
$button = $null
while ([DateTime]::UtcNow -lt $deadline) {
  $process = Get-Process -Id $ProcessId -ErrorAction Stop
  $window = $process.MainWindowHandle
  if ($window -ne [IntPtr]::Zero) {
    $root = [System.Windows.Automation.AutomationElement]::FromHandle($window)
    $nameCondition = [System.Windows.Automation.PropertyCondition]::new([System.Windows.Automation.AutomationElement]::NameProperty, 'About')
    $typeCondition = [System.Windows.Automation.PropertyCondition]::new([System.Windows.Automation.AutomationElement]::ControlTypeProperty, [System.Windows.Automation.ControlType]::Button)
    $condition = [System.Windows.Automation.AndCondition]::new($nameCondition, $typeCondition)
    $button = $root.FindFirst([System.Windows.Automation.TreeScope]::Descendants,$condition)
    if ($button) { break }
  }
  Start-Sleep -Milliseconds 500
}
if (!$button) {
  $named=$root.FindAll([System.Windows.Automation.TreeScope]::Descendants,$nameCondition)
  $types=@($named | ForEach-Object { $_.Current.ControlType.ProgrammaticName })
  throw "Production webview did not expose its About Button control. Named controls: $($types -join ', ')"
}
if ($button.Current.IsOffscreen -or !$button.Current.IsEnabled) { throw 'Owned About button is not visibly usable' }
$invoke = $null
if ($button.TryGetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern, [ref]$invoke)) {
  $invoke.Invoke()
} else {
  # WebView2 can expose the semantic Button without an Invoke provider.
  # Use a real pointer click only after proving foreground/window ownership.
  Add-Type @"
using System;
using System.Runtime.InteropServices;
public static class MMNativePointer {
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr window);
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr window, int command);
  [DllImport("user32.dll")] public static extern bool BringWindowToTop(IntPtr window);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr window, out uint process);
  [DllImport("kernel32.dll")] public static extern uint GetCurrentThreadId();
  [DllImport("user32.dll")] public static extern bool AttachThreadInput(uint from, uint to, bool attach);
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [StructLayout(LayoutKind.Sequential)] public struct MOUSEINPUT {
    public int dx, dy; public uint mouseData, flags, time; public UIntPtr extra;
  }
  [StructLayout(LayoutKind.Sequential)] public struct KEYBDINPUT {
    public ushort key, scan; public uint flags, time; public UIntPtr extra;
  }
  [StructLayout(LayoutKind.Explicit)] public struct INPUTUNION {
    [FieldOffset(0)] public MOUSEINPUT mouse;
    [FieldOffset(0)] public KEYBDINPUT keyboard;
  }
  [StructLayout(LayoutKind.Sequential)] public struct INPUT {
    public uint type; public INPUTUNION data;
  }
  [DllImport("user32.dll", SetLastError=true)] public static extern uint SendInput(uint count, INPUT[] input, int size);
  public static void ReleaseForegroundLock() {
    var input = new INPUT[2];
    input[0].type = input[1].type = 1;
    input[0].data.keyboard.key = input[1].data.keyboard.key = 0x12;
    input[1].data.keyboard.flags = 2;
    if (SendInput(2, input, Marshal.SizeOf(typeof(INPUT))) != 2)
      throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error(), "Native ALT input failed");
  }
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint flags, uint dx, uint dy, uint data, UIntPtr extra);
}
"@
  [uint32]$owner = 0
  $ownedThread=[MMNativePointer]::GetWindowThreadProcessId($window,[ref]$owner)
  if ($owner -ne $ProcessId) { throw 'Production window ownership changed before focus' }
  $callerThread=[MMNativePointer]::GetCurrentThreadId()
  $attached=$false
  try {
    if ($callerThread -ne $ownedThread) { $attached=[MMNativePointer]::AttachThreadInput($callerThread,$ownedThread,$true) }
    [void][MMNativePointer]::ShowWindow($window,9)
    [void][MMNativePointer]::BringWindowToTop($window)
    [void][MMNativePointer]::SetForegroundWindow($window)
    $focusDeadline=[DateTime]::UtcNow.AddSeconds(5)
    while ([MMNativePointer]::GetForegroundWindow() -ne $window -and [DateTime]::UtcNow -lt $focusDeadline) {
      [void][MMNativePointer]::SetForegroundWindow($window)
      Start-Sleep -Milliseconds 100
    }
  } finally {
    if ($attached) { [void][MMNativePointer]::AttachThreadInput($callerThread,$ownedThread,$false) }
  }
  if ([MMNativePointer]::GetForegroundWindow() -ne $window) {
    # Windows enables foreground changes after ALT input (LockSetForegroundWindow
    # documentation). This is a real key press/release on the disposable runner,
    # not a persistent setting change. Never click before foreground is proved.
    [MMNativePointer]::ReleaseForegroundLock()
    [void][MMNativePointer]::SetForegroundWindow($window)
    $focusDeadline=[DateTime]::UtcNow.AddSeconds(5)
    while ([MMNativePointer]::GetForegroundWindow() -ne $window -and [DateTime]::UtcNow -lt $focusDeadline) {
      [void][MMNativePointer]::SetForegroundWindow($window)
      Start-Sleep -Milliseconds 100
    }
  }
  if ([MMNativePointer]::GetForegroundWindow() -ne $window) {
    $foreground=[MMNativePointer]::GetForegroundWindow()
    [uint32]$foregroundOwner=0
    [void][MMNativePointer]::GetWindowThreadProcessId($foreground,[ref]$foregroundOwner)
    throw "Owned production window could not become foreground: owned=$window foreground=$foreground foregroundPID=$foregroundOwner session=$($process.SessionId)"
  }
  $bounds=$root.Current.BoundingRectangle
  $rect=$button.Current.BoundingRectangle
  $x=[int]($rect.Left+$rect.Width/2);$y=[int]($rect.Top+$rect.Height/2)
  if ($rect.Width -le 0 -or $rect.Height -le 0 -or $x -lt $bounds.Left -or $x -ge $bounds.Right -or $y -lt $bounds.Top -or $y -ge $bounds.Bottom) { throw 'About click is outside the owned window' }
  if (![MMNativePointer]::SetCursorPos($x,$y)) { throw 'Owned pointer positioning failed' }
  [MMNativePointer]::mouse_event(2,0,0,0,[UIntPtr]::Zero)
  [MMNativePointer]::mouse_event(4,0,0,0,[UIntPtr]::Zero)
}
$deadline = [DateTime]::UtcNow.AddSeconds(20)
$verified = $false
while ([DateTime]::UtcNow -lt $deadline) {
  $elements=$root.FindAll([System.Windows.Automation.TreeScope]::Descendants,[System.Windows.Automation.Condition]::TrueCondition)
  $names=@($elements | ForEach-Object { $_.Current.Name })
  if (($names -contains $Version) -and ($names -contains 'Apache License 2.0')) { $verified=$true; break }
  Start-Sleep -Milliseconds 300
}
if (!$verified) { throw "Production About did not report version $Version and Apache 2.0. Visible names: $($names -join ' | ')" }
& pwsh.exe -NoProfile -NonInteractive -File "$PSScriptRoot/screenshot.ps1" -ProcessId $ProcessId -OutputPath $OutputPath
if ($LASTEXITCODE -ne 0) { throw 'Owned window screenshot failed' }
Write-Host 'PASS actual production About, build version, license and owned screenshot'
