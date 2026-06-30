```
/pogolo - decentralize or die/
```

solo db-less bitcoin-only mining pool  
meant for lan swarms, not the internet  
think of this as public-pool but minimal and for self-sovereign nerds  
infinite thanks to btcd for bitcoin tooling and public-pool for reference  
start it, point your miners to it, and watch the logs roll by

![cpuminer on regtest](./showoff.png)

_want a go pool you can run on the public internet? check out https://github.com/Distortions81/M45-Core-goPool!_


- [features](#features)
- [install](#install)
    - [umbrel](#umbrel)
    - [docker](#docker)
    - [go](#go)
    - [manual install](#manual-install)


# features

- set a default address to mine to, no more ctrl+c ctrl+v ctrl+v ctrl+v ctrl+v ctrl+v
- colorful and detailed logs (respects NO_COLOR)
- read-only api (see api.go and swagger spec)
- zmq and btcd websocket support
- up to 160k shares/sec

## implemented BIPs

- 34
- 41 (probably)
- 54 (coinbase, relies on a compliant backend for the rest)
- 310 (minus `subscribe-extranonce`)
- 323

## supported miners

- \*axe family (bitaxe, nerd\*axe, etc)
- cpuminer
- antminers

## supported backends

pogolo has no tls support.

- JSON-RPC (core, knots, etc)
- btcd websocket
- ZMQ (`hashblock` topic)

## unsupported

- miners requiring extranonce.subscribe
- luckyminers/other non-foss axe clones
- stratum v2 (temporarily)

# install

## umbrel

get pogolo from the umbrel app store: https://apps.umbrel.com/app/pogolo  
then point your miners to stratum+tcp://umbrel.local:5661

## docker

image at https://hub.docker.com/r/0xf0xx0/pogolo

copy the pogolo.toml from [`contrib/pkgs/docker`](./contrib/pkgs/docker), configure it to your needs, then run

```sh
docker run -d -v "./pogolo.toml:/config/pogolo.toml" 0xf0xx0/pogolo:latest
```

docker (+compose) files exist and work but likely need tweaks, theyre also under [`contrib/pkgs/docker`](./contrib/pkgs/docker)

### environment overrides

for easy config

```yaml
POGOLO_HOST: # overrides [pogolo].host
POGOLO_BACKEND_HOST: # overrides [backend].host
POGOLO_BACKEND_RPCAUTH: # overrides [backend].rpcauth
POGOLO_BACKEND_ZMQHOST: # overrides [backend].zmq_host
```

## go

```sh
go install git.0xf0xx0.eth.limo/0xf0xx0/pogolo@latest
```

then follow [manual install/configure](#configure).

## manual install

### build

```sh
git clone https://git.0xf0xx0.eth.limo/0xf0xx0/pogolo.git
cd pogolo
go build -trimpath -ldflags="-s -w -buildid="
go install
```

### configure

```sh
# create config dir
mkdir "$XDG_CONFIG_HOME/pogolo"

# write embedded default config (preferred)
pogolo --writedefaultconf "$XDG_CONFIG_HOME/pogolo/pogolo.toml"
# OR copy the example config
cp contrib/pogolo.example.toml "$XDG_CONFIG_HOME/pogolo/pogolo.toml"

# configure interface, backend, and auth
vim "$XDG_CONFIG_HOME/pogolo/pogolo.toml"

# run
pogolo
```
theres a systemd unit at [`contrib/init/pogolo.service`](./contrib/init/pogolo.service) 
for bare installs  
it expects pogolo to be in the system PATH, a `bitcoin` group, and a config file at `/etc/pogolo/pogolo.toml`.
```sh
sudo cp contrib/init/pogolo.service /etc/systemd/system/
sudo mkdir -p /etc/pogolo
pogolo --writedefaultconf /etc/pogolo/pogolo.toml
sudo chmod -R 755 /etc/pogolo
sudo chown -R :bitcoin /etc/pogolo 
sudo systemctl enable --now pogolo
```

# miner config

host: stratum+tcp://\<your lan ip\>:5661  
username: \<your onchain address\>[.\<worker name\>]  
password: [required if set in pogolo.toml]  
suggested diff: [optional]

the onchain address is also optional if the pool has a default configured.  
shares submitted between job changes are normal, the expected error rate is <=0.2%.

# benchmarking

to reproduce shares/s result:  
benchmark with https://github.com/eandersson/open-pool-benchmark

# TODO

- link stratum and mining docs  
   waiting for bip 41  
   https://web.archive.org/web/20201130051808/https://braiins.com/stratum-v1/docs
- option to add workername to coinbase scriptsig?
- prometheus metrics
    - block submission metrics
- aur packages (needs a maintainer)
