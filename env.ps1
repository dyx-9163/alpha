$env:Path = "D:\tools\node;D:\tools\node-global;D:\tools\go\bin;D:\tools\gopath\bin;$env:Path"
$env:GOROOT = "D:\tools\go"
$env:GOPATH = "D:\tools\gopath"
$env:GOCACHE = "D:\tools\gocache"
$env:NPM_CONFIG_PREFIX = "D:\tools\node-global"
Write-Host "AIFAR toolchain loaded."
node --version
npm --version
pnpm --version
go version
