@echo off
set "PATH=D:\tools\node;D:\tools\node-global;D:\tools\go\bin;D:\tools\gopath\bin;%PATH%"
set "GOROOT=D:\tools\go"
set "GOPATH=D:\tools\gopath"
set "GOCACHE=D:\tools\gocache"
set "NPM_CONFIG_PREFIX=D:\tools\node-global"
echo AIFAR toolchain loaded.
node --version
npm --version
pnpm --version
go version
