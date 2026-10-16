@echo off
rem Builds every command under cmd\ into build\ at the repository root, for
rem Windows developers without make. Arguments are passed to go build, for
rem example: tools\build.cmd -trimpath
setlocal
pushd "%~dp0.." || exit /b 1
go build %* -o build/ ./cmd/...
set status=%errorlevel%
popd
exit /b %status%
