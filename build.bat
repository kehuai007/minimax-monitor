@echo off
setlocal
taskkill /IM minimax-monitor.exe >nUL 2>&1
ping -n 6 127.0.0.1 >nUL
taskkill /F /IM minimax-monitor.exe >nUL 2>&1
if not exist dist mkdir dist
go build -trimpath -ldflags="-s -w" -o dist\minimax-monitor.exe .\cmd\minimax-monitor
if errorlevel 1 (
  echo [build] FAILED
  exit /b 1
)
echo [build] OK -^> dist\minimax-monitor.exe
endlocal