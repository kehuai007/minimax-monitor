@echo off
setlocal enabledelayedexpansion
if not exist dist mkdir dist
set "TARGETS=linux-amd64 linux-arm64 darwin-arm64 windows-amd64"
for %%T in (%TARGETS%) do (
  for /f "tokens=1,2 delims=-" %%A in ("%%T") do (
    set "GOOS=%%A"
    set "GOARCH=%%B"
    set "EXT="
    if "%%A"=="windows" set "EXT=.exe"
    echo [build] %%T ...
    set "CGO_ENABLED=0"
    go build -trimpath -ldflags="-s -w" -o dist\minimax-monitor-%%T!EXT! .\cmd\minimax-monitor || exit /b 1
  )
)
echo [build] all OK
endlocal