@echo off
setlocal enabledelayedexpansion

echo ========================================
echo Auto Commit, Version Increment, and Push
echo ========================================

:: 1. Menambahkan semua perubahan
echo.
echo [1/4] Menambahkan file yang berubah (git add .)...
git add .

:: 2. Meminta input pesan commit dan melakukan commit
set /p commit_msg="Masukkan pesan commit (tekan Enter untuk menggunakan waktu saat ini): "
if "%commit_msg%"=="" (
    set commit_msg=Auto commit: %date% %time%
)
echo.
echo [2/4] Melakukan commit...
git commit -m "!commit_msg!"

:: 3. Mengambil tag terakhir dan menaikkan versinya (Increment patch version)
echo.
echo [3/4] Mengambil versi tag terakhir dan membuat tag baru...
git fetch --tags origin 2>nul
set CURRENT_TAG=
for /f "tokens=*" %%a in ('git describe --tags --abbrev^=0 2^>nul') do set CURRENT_TAG=%%a

if "%CURRENT_TAG%"=="" goto no_tag

echo Tag saat ini: %CURRENT_TAG%

:: Membuat script PowerShell sementara untuk menghitung versi berikutnya
echo $t = '%CURRENT_TAG%' > get_next_tag.ps1
echo if ($t -match '^^v?\.?(\d+)\.(\d+)\.(\d+)') { >> get_next_tag.ps1
echo     $newPatch = [int]$matches[3] + 1 >> get_next_tag.ps1
echo     Write-Output ('v' + $matches[1] + '.' + $matches[2] + '.' + $newPatch) >> get_next_tag.ps1
echo } else { >> get_next_tag.ps1
echo     Write-Output 'v1.5.2' >> get_next_tag.ps1
echo } >> get_next_tag.ps1

:: Menjalankan script sementara dan menangkap outputnya
for /f "usebackq tokens=*" %%i in (`powershell -NoProfile -ExecutionPolicy Bypass -File get_next_tag.ps1`) do set NEW_TAG=%%i

:: Menghapus script sementara
del get_next_tag.ps1

echo Tag baru: !NEW_TAG!
goto after_tag

:no_tag
set NEW_TAG=v1.2.0
echo Tag belum ada, memulai dari versi !NEW_TAG!

:after_tag

:: Membuat tag baru
git tag !NEW_TAG!

:: 4. Mendorong (push) kode beserta tag ke GitHub
echo.
echo [4/4] Mendorong kode dan tag ke GitHub (git push origin HEAD --tags)...
git push origin HEAD
git push origin !NEW_TAG!

echo.
echo ========================================
echo Selesai: Kode dan tag (!NEW_TAG!) berhasil di-push.
echo ========================================
pause
endlocal
