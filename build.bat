@echo off
setlocal
if not exist dist mkdir dist
go build -trimpath -ldflags="-s -w" -o dist\minimax-monitor.exe .\cmd\minimax-monitor
if errorlevel 1 (
  echo [build] FAILED
  exit /b 1
)
echo [build] OK -^> dist\minimax-monitor.exe
endlocal