@echo off
set GOROOT=C:\go
set PATH=C:\go\bin;%PATH%
cd /d "F:\Work Document\project\sessionBridge\go-core"
echo Building...
C:\go\bin\go.exe build ./cmd/node/ 2> build_err.txt
set EXIT_CODE=%ERRORLEVEL%
echo EXIT CODE: %EXIT_CODE% > build_result.txt
type build_err.txt >> build_result.txt
echo Done.
exit /b %EXIT_CODE%
