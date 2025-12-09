```
/pogolo - decentralize or die/
```

solo db-less bitcoin-only mining pool  
meant for lan swarms, not the internet  
think of this as public-pool but minimal and for self-sovereign nerds  
infinite thanks to btcd for bitcoin tooling and public-pool for reference  
start it, point your miners to it, and watch the logs roll by

## features

- api (see api.go, swagger spec eventually in 1.0.0)
- set a default address to mine to, no more ctrl+c ctrl+v ctrl+v ctrl+v ctrl+v ctrl+v
- btcd websocket support
- ~~longpolling (i think)~~ borked
- 15-17ms share processing latency, up to 4.3-5.2k shares/sec

## supported BIPs
- 34
- 41 (probably)
- 310 (minus `info` and `subscribe-extranonce`)
- 320

## supported miners

- \*axe family (bitaxe, nerd\*axe)
- cpuminer


### supported backends

- bitcoin core (http POST)
- btcd (http and ws, no tls support)

## unsupported

- miners requiring extranonce.subscribe
- luckyminers

# setup

## install

```sh
git clone https://github.com/0xf0xx0/pogolo.git
cd pogolo
go build
go install
# config
mkdir $XDG_CONFIG_HOME/pogolo
pogolo --writedefaultconf $XDG_CONFIG_HOME/pogolo/pogolo.toml # or copy pogolo.example.toml to `$XDG_CONFIG_HOME/pogolo/pogolo.toml`
# then configure the interface, backend, and auth
```

## run

```sh
pogolo
```

## miner config

```
host: stratum+tcp://<your lan ip>:5661
username: <your onchain address>[.<worker name>]
password: [required if set in pogolo.toml]
suggested diff: [optional]
```
the onchain address is also optional if the pool has a default configured.

## benchmarking
to reproduce shares/s result:
1. set `benchmark = true` in your config
2. connect with cpuminer (preferably from a different machine) and watch the shares fly

## TODO

- document code for future viewers
    - link stratum and mining docs  
      waiting for bip 41  
      https://web.archive.org/web/20201130051808/https://braiins.com/stratum-v1/docs
- logs with personality
    - hide cursor, use full term height?
    - styled around standard 80x24
    - tui?? idk i need a tui dev lol
- completion
- option to set bits for signalling support
- option to ignore empty templates?
- db interface for optional stats persistence?
- special bitaxe functions like auto-restart on flatline and hashrate polling?
- stratum keepalive?
- zmq for block notifs?
- btcd tls? yuck
- when 1.0.0:
    - docker image
    - aur packages
