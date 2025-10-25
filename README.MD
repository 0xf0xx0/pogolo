solo db-less bitcoin-only mining pool  
meant for lan swarms, not the internet  
think of this as public-pool but minimal and for self-sovereign nerds  
infinite thanks to btcd for bitcoin tooling and public-pool for reference  
start it, point your miners to it, and watch the logs roll by

## features

- api (both our own and a semi-compatible public-pool api)
  see api.go, swagger spec eventually in 1.0.0
- public-pool-ui compat (is wip and likely wont ever be 1:1, but the main page lists stats uwu)
- set a default address to mine to poolside, no more ctrl+c ctrl+v ctrl+v ctrl+v ctrl+v ctrl+v

## supported

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
pogolo --writedefaultconf # or copy pogolo.example.toml to `$XDG_CONFIG_HOME/pogolo/pogolo.toml`
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
the address is also optional if the pool has a default address configured.

## TODO

- document code for future viewers
    - link stratum and mining docs  
      waiting for bip 41
- logs with personality
    - hide cursor, use full term height?
    - styled around standard 80x24
    - tui?? idk i need a tui dev lol
- option to ignore empty templates?
- 0.5% dev fee for large miners (>=50th), optionally scaling up to 1.5 for >= 50PH?
- db interface for optional stats persistence?
- special bitaxe functions like auto-restart on flatline and hashrate polling?
- extranonce subscription?
- stratum keepalive?
- zmq for block notifs?
- btcd tls? yuck
