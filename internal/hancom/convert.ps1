# Converts one document with Hancom Office. Run by why-hwp (internal/hancom),
# which passes every path through environment variables so no path is ever
# parsed as PowerShell code:
#
#   WHY_HWP_IN           input file, inside the temporary folder
#   WHY_HWP_OUT          output file, inside the temporary folder
#   WHY_HWP_OPEN_FORMAT  format name for Open ("" lets Hwp detect it)
#   WHY_HWP_SAVE_FORMAT  format name for SaveAs (HWP, HWPX, PDF, TEXT)
#
# Hwp asks the user before it writes outside the temporary folder, so the
# caller copies files in and out of it.
#
# Output lines on stdout:
#   pid <n>[,<n>...]       Hwp processes that appeared while the COM object
#                          was created (the instance this script owns)
#   registermodule <bool>  result of RegisterModule for FilePathCheckerModule
# Exit codes: 0 done, 1 conversion failed, 3 the COM object cannot be created.
# This file must stay ASCII: Windows PowerShell 5.1 reads it as ANSI.

$ErrorActionPreference = 'Stop'

function Out-Line([string]$s) {
    [Console]::Out.WriteLine($s)
    [Console]::Out.Flush()
}

$in = $env:WHY_HWP_IN
$out = $env:WHY_HWP_OUT
$openFormat = [string]$env:WHY_HWP_OPEN_FORMAT
$saveFormat = $env:WHY_HWP_SAVE_FORMAT

$before = @(Get-Process -Name Hwp -ErrorAction SilentlyContinue | ForEach-Object { $_.Id })
try {
    $hwp = New-Object -ComObject HWPFrame.HwpObject
} catch {
    [Console]::Error.WriteLine('cannot create HWPFrame.HwpObject: ' + $_.Exception.Message)
    exit 3
}
$after = @(Get-Process -Name Hwp -ErrorAction SilentlyContinue | ForEach-Object { $_.Id })
$mine = @($after | Where-Object { $before -notcontains $_ })
Out-Line ('pid ' + ($mine -join ','))

$code = 0
try {
    $hwp.XHwpWindows.Item(0).Visible = $false
    $reg = $false
    try { $reg = [bool]$hwp.RegisterModule('FilePathCheckDLL', 'FilePathCheckerModule') } catch { }
    Out-Line ('registermodule ' + $reg)

    # The window is hidden, so a message box would wait forever. Saving a
    # document with tracked changes as PDF or TEXT asks "save in this format?";
    # 0x10000 answers such Yes/No boxes with Yes.
    $hwp.SetMessageBoxMode(0x10000) | Out-Null

    if (-not $hwp.Open($in, $openFormat, 'forceopen:true')) {
        throw 'Hwp could not open the input'
    }
    $target = $out
    if ($saveFormat -eq 'TEXT') { $target = $out + '.raw' }
    if (-not $hwp.SaveAs($target, $saveFormat, '')) {
        throw ('Hwp SaveAs ' + $saveFormat + ' failed')
    }
    if ($saveFormat -eq 'TEXT') {
        # Hwp writes text in the ANSI code page (CP949); store it as UTF-8.
        $b = [IO.File]::ReadAllBytes($target)
        if ($b.Length -ge 3 -and $b[0] -eq 0xEF -and $b[1] -eq 0xBB -and $b[2] -eq 0xBF) {
            $text = [Text.Encoding]::UTF8.GetString($b, 3, $b.Length - 3)
        } elseif ($b.Length -ge 2 -and $b[0] -eq 0xFF -and $b[1] -eq 0xFE) {
            $text = [Text.Encoding]::Unicode.GetString($b, 2, $b.Length - 2)
        } else {
            $text = [Text.Encoding]::GetEncoding(949).GetString($b)
        }
        [IO.File]::WriteAllText($out, $text, (New-Object Text.UTF8Encoding $false))
    }
} catch {
    [Console]::Error.WriteLine($_.Exception.Message)
    $code = 1
} finally {
    try { [void]$hwp.Clear(1) } catch { }
    try { $hwp.Quit() } catch { }
    [void][Runtime.InteropServices.Marshal]::ReleaseComObject($hwp)
}
exit $code
