# Builds the fake test documents testdata/sample.hwpx and testdata/sample.hwp
# with the Hancom Office COM object (HWPFrame.HwpObject). Windows only.
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File testdata\gen\make-fixtures.ps1
#
# Every word in the documents is made up. The user name Hwp records as
# author, last saver and memo author is replaced, and the script fails if
# it is still found in either file. Only the Hwp instance started by this
# script is closed; Hwp windows that were already open are left alone.

param(
    [string]$OutDir = (Join-Path $PSScriptRoot '..')
)

$ErrorActionPreference = 'Stop'
$OutDir = (Resolve-Path $OutDir).Path

function Insert-Text($hwp, [string]$text) {
    $act = $hwp.CreateAction('InsertText')
    $set = $act.CreateSet()
    [void]$act.GetDefault($set)
    $set.SetItem('Text', $text)
    [void]$act.Execute($set)
}

function Add-Para($hwp, [string]$text) {
    Insert-Text $hwp $text
    [void]$hwp.Run('BreakPara')
}

function New-Table($hwp, [int]$rows, [int]$cols) {
    $act = $hwp.CreateAction('TableCreate')
    $set = $act.CreateSet()
    [void]$act.GetDefault($set)
    $set.SetItem('Rows', $rows)
    $set.SetItem('Cols', $cols)
    $set.SetItem('WidthType', 0)
    $set.SetItem('HeightType', 0)
    [void]$act.Execute($set)
}

function New-Png([string]$path) {
    Add-Type -AssemblyName System.Drawing
    $bmp = New-Object System.Drawing.Bitmap 64, 32
    $g = [System.Drawing.Graphics]::FromImage($bmp)
    $g.Clear([System.Drawing.Color]::LightSteelBlue)
    $g.FillRectangle([System.Drawing.Brushes]::DarkSlateBlue, 8, 8, 48, 16)
    $g.Dispose()
    $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png)
    $bmp.Dispose()
}

$Placeholder = 'why-hwp'

# .NET Framework's ZipArchive deflates even with NoCompression, but HWPX
# needs mimetype stored, so the package is written by this small writer.
Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.IO;
using System.IO.Compression;
using System.Text;

public class FixtureZipWriter {
    static readonly uint[] table = MakeTable();
    readonly Stream w;
    readonly MemoryStream central = new MemoryStream();
    int count;

    public FixtureZipWriter(Stream s) { w = s; }

    static uint[] MakeTable() {
        var t = new uint[256];
        for (uint i = 0; i < 256; i++) {
            uint c = i;
            for (int k = 0; k < 8; k++) c = (c & 1) != 0 ? 0xEDB88320u ^ (c >> 1) : c >> 1;
            t[i] = c;
        }
        return t;
    }

    static uint Crc(byte[] b) {
        uint c = 0xFFFFFFFFu;
        foreach (byte x in b) c = table[(c ^ x) & 0xFF] ^ (c >> 8);
        return c ^ 0xFFFFFFFFu;
    }

    static void U16(Stream s, int v) { s.WriteByte((byte)v); s.WriteByte((byte)(v >> 8)); }
    static void U32(Stream s, uint v) { U16(s, (int)(v & 0xFFFF)); U16(s, (int)(v >> 16)); }

    public void Add(string name, byte[] data, bool store, DateTime t) {
        byte[] body = data;
        if (!store) {
            var ms = new MemoryStream();
            using (var d = new DeflateStream(ms, CompressionLevel.Optimal, true)) d.Write(data, 0, data.Length);
            body = ms.ToArray();
        }
        if (t.Year < 1980) t = new DateTime(1980, 1, 1);
        int dosTime = (t.Hour << 11) | (t.Minute << 5) | (t.Second / 2);
        int dosDate = ((t.Year - 1980) << 9) | (t.Month << 5) | t.Day;
        byte[] n = Encoding.UTF8.GetBytes(name);
        uint crc = Crc(data);
        uint offset = (uint)w.Position;
        int method = store ? 0 : 8;

        U32(w, 0x04034b50); U16(w, 20); U16(w, 0); U16(w, method); U16(w, dosTime); U16(w, dosDate);
        U32(w, crc); U32(w, (uint)body.Length); U32(w, (uint)data.Length); U16(w, n.Length); U16(w, 0);
        w.Write(n, 0, n.Length);
        w.Write(body, 0, body.Length);

        var c = central;
        U32(c, 0x02014b50); U16(c, 20); U16(c, 20); U16(c, 0); U16(c, method); U16(c, dosTime); U16(c, dosDate);
        U32(c, crc); U32(c, (uint)body.Length); U32(c, (uint)data.Length); U16(c, n.Length); U16(c, 0); U16(c, 0);
        U16(c, 0); U16(c, 0); U32(c, 0); U32(c, offset);
        c.Write(n, 0, n.Length);
        count++;
    }

    public void Finish() {
        uint start = (uint)w.Position;
        central.Position = 0;
        central.CopyTo(w);
        U32(w, 0x06054b50); U16(w, 0); U16(w, 0); U16(w, count); U16(w, count);
        U32(w, (uint)central.Length); U32(w, start); U16(w, 0);
    }
}
'@ -ReferencedAssemblies System.IO.Compression

# Remove-UserName copies the HWPX package $src to $dst with the author,
# last-saved-by and memo author fields set to $Placeholder. Entry order,
# timestamps and stored/deflated methods are kept, so mimetype stays the
# first, uncompressed entry. Returns the names that were replaced.
function Remove-UserName([string]$src, [string]$dst) {
    Add-Type -AssemblyName System.IO.Compression, System.IO.Compression.FileSystem
    $utf8 = New-Object System.Text.UTF8Encoding $false
    $in = [System.IO.Compression.ZipFile]::OpenRead($src)
    try {
        $hpf = $in.GetEntry('Contents/content.hpf')
        $r = New-Object System.IO.StreamReader($hpf.Open(), $utf8)
        $text = $r.ReadToEnd()
        $r.Dispose()
        $names = @([regex]::Matches($text, '<opf:meta name="(?:creator|lastsaveby)" content="text">([^<]+)</opf:meta>') |
            ForEach-Object { $_.Groups[1].Value } | Where-Object { $_ -ne $Placeholder } | Sort-Object -Unique)

        $fs = [IO.File]::Open($dst, [IO.FileMode]::CreateNew)
        $out = New-Object FixtureZipWriter($fs)
        try {
            foreach ($e in $in.Entries) {
                $rs = $e.Open()
                $ms = New-Object IO.MemoryStream
                $rs.CopyTo($ms)
                $rs.Dispose()
                $bytes = $ms.ToArray()
                if ($e.FullName -match '\.(xml|hpf|rdf)$') {
                    $s = $utf8.GetString($bytes)
                    $s = [regex]::Replace($s, '(<opf:meta name="(?:creator|lastsaveby)" content="text">)[^<]*(</opf:meta>)', "`${1}$Placeholder`${2}")
                    $s = [regex]::Replace($s, '(<hp:stringParam name="Author">)[^<]*(</hp:stringParam>)', "`${1}$Placeholder`${2}")
                    foreach ($n in $names) {
                        $s = $s.Replace("/$n/", "/$Placeholder/")
                    }
                    $bytes = $utf8.GetBytes($s)
                }
                $out.Add($e.FullName, $bytes, ($e.CompressedLength -eq $e.Length), $e.LastWriteTime.DateTime)
            }
            $out.Finish()
        }
        finally {
            $fs.Dispose()
        }
    }
    finally {
        $in.Dispose()
    }
    return $names
}

# Hide-UserNameInHwp overwrites $names in the HWP file's summary information
# (author and last-saved-by, which Hwp fills in on every save) with the
# same number of 'x' characters. The stream is uncompressed UTF-16LE, so an
# equal-length overwrite keeps every size and offset in the file valid.
function Hide-UserNameInHwp([string]$path, [string[]]$names) {
    $b = [IO.File]::ReadAllBytes($path)
    $latin = [Text.Encoding]::GetEncoding(28591)
    foreach ($n in $names) {
        $pat = [Text.Encoding]::Unicode.GetBytes($n)
        $rep = [Text.Encoding]::Unicode.GetBytes(('x' * $n.Length))
        $s = $latin.GetString($b)
        $p = $latin.GetString($pat)
        $i = $s.IndexOf($p, [StringComparison]::Ordinal)
        while ($i -ge 0) {
            [Array]::Copy($rep, 0, $b, $i, $rep.Length)
            $i = $s.IndexOf($p, $i + $pat.Length, [StringComparison]::Ordinal)
        }
    }
    [IO.File]::WriteAllBytes($path, $b)
}

# Assert-NoName fails if any of $names is left in $path, either as a plain
# file (HWP) or inside any zip entry (HWPX), in UTF-8 or UTF-16LE.
function Assert-NoName([string]$path, [string[]]$names) {
    $blobs = @()
    $all = [IO.File]::ReadAllBytes($path)
    $blobs += , $all
    if ($path -like '*.hwpx') {
        $zip = [System.IO.Compression.ZipFile]::OpenRead($path)
        try {
            foreach ($e in $zip.Entries) {
                $ms = New-Object IO.MemoryStream
                $rs = $e.Open(); $rs.CopyTo($ms); $rs.Dispose()
                $blobs += , $ms.ToArray()
            }
        }
        finally { $zip.Dispose() }
    }
    $latin = [Text.Encoding]::GetEncoding(28591)
    foreach ($n in $names) {
        $pats = @($latin.GetString([Text.Encoding]::UTF8.GetBytes($n)), $latin.GetString([Text.Encoding]::Unicode.GetBytes($n)))
        foreach ($b in $blobs) {
            $s = $latin.GetString($b)
            foreach ($p in $pats) {
                if ($s.Contains($p)) { throw "user name still present in $path" }
            }
        }
    }
}

# Hwp shows a blocking "allow file access" dialog when the automation object
# writes outside the temp directory, so build in a temp directory and copy
# the results out.
$work = Join-Path ([IO.Path]::GetTempPath()) ('why-hwp-fixture-' + [guid]::NewGuid().ToString('N'))
[void](New-Item -ItemType Directory -Path $work)
$png = Join-Path $work 'figure.png'
New-Png $png

$hwp = New-Object -ComObject HWPFrame.HwpObject
try {
    # Returns false when the checker DLL is not installed; saving to a new
    # path still works without it.
    [void]$hwp.RegisterModule('FilePathCheckDLL', 'FilePathCheckerModule')

    Add-Para $hwp '샘플 사업 계획서'
    Add-Para $hwp '1. 개요'
    Add-Para $hwp '이 문서는 why-hwp 시험용으로 만든 가짜 문서다. 실제 내용은 없다.'
    Add-Para $hwp '2. 추진 일정 및 담당'

    New-Table $hwp 3 3
    $cells = @(
        '항목', '내용', '담당',
        '요구사항 정리', '범위와 일정 확정', '(A)',
        '시제품 개발', '핵심 기능 구현', '(A/B)'
    )
    for ($i = 0; $i -lt $cells.Count; $i++) {
        Insert-Text $hwp $cells[$i]
        if ($i -lt $cells.Count - 1) { [void]$hwp.Run('TableRightCell') }
    }
    [void]$hwp.Run('Cancel')
    [void]$hwp.MovePos(3, 0, 0)
    [void]$hwp.Run('BreakPara')

    Add-Para $hwp '3. 기대 효과'
    Insert-Text $hwp '문서 구조를 프로그램이 읽을 수 있다 (가).'
    [void]$hwp.Run('InsertFieldMemo')
    Insert-Text $hwp '가짜 메모: 표현을 다듬을 것'
    [void]$hwp.Run('Cancel')
    [void]$hwp.MovePos(3, 0, 0)
    [void]$hwp.Run('BreakPara')

    Add-Para $hwp '그림 1. 가짜 도형'
    [void]$hwp.InsertPicture($png, $true, 0, $false, $false, 0, 0, 0)
    [void]$hwp.MovePos(3, 0, 0)

    # Hwp stamps the user name of this PC into the document. Save as HWPX,
    # replace the name there, then let Hwp convert the cleaned HWPX to HWP.
    $raw = Join-Path $work 'raw.hwpx'
    if (-not $hwp.SaveAs($raw, 'HWPX', '')) { throw "SaveAs failed: $raw" }
    $hwpx = Join-Path $work 'sample.hwpx'
    $names = Remove-UserName $raw $hwpx
    $hwp.Clear(1)
    if (-not $hwp.Open($hwpx, 'HWPX', 'forceopen:true')) { throw "Open failed: $hwpx" }
    $hwpb = Join-Path $work 'sample.hwp'
    if (-not $hwp.SaveAs($hwpb, 'HWP', '')) { throw "SaveAs failed: $hwpb" }
    $hwp.Clear(1)
    Hide-UserNameInHwp $hwpb $names

    foreach ($f in @($hwpx, $hwpb)) {
        Assert-NoName $f $names
        $dst = Join-Path $OutDir (Split-Path $f -Leaf)
        Copy-Item -LiteralPath $f -Destination $dst -Force
        "wrote $dst"
    }
}
finally {
    $hwp.Clear(1)
    $hwp.Quit()
    [void][Runtime.InteropServices.Marshal]::ReleaseComObject($hwp)
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
