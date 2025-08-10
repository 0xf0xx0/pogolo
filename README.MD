solo db-less bitcoin-only mining pool  
meant for lan swarms, not the internet  
think of this as public-pool but minimal and for self-sovereign nerds  
infinite thanks to btcd for bitcoin tooling and public-pool for reference  
start it, point your miners to it, and watch the logs roll by

# setup
## install
```
# latest release
go install github.com/0xf0xx0/pogolo@latest
# or from git
go install github.com/0xf0xx0/pogolo@logopo
```

## run
```
pogolo
```

## server config
by default, pogolo runs on regtest and listens on localhost v4 and v6.  
see [pogolo.example.toml](./pogolo.example.toml) for config docs.  
the default path is $XDG_CONFIG_HOME/pogolo/pogolo.toml, use `--conf` for a custom path

## miner config
```
host: stratum+tcp://<your lan ip>:5661
username: <your onchain address>[.<worker name>]
password: [required if set in pogolo.toml]
suggested diff: [optional]
```

## supported
- *axe family
- cpuminer
### supported backends
- bitcoind
- btcd (http and ws, disable tls)
## unsupported
- miners needing extranonce.subscribe
- luckyminers

TODO
- document code for future viewers
    - link stratum and mining docs
        waiting for bip 41
- logs with personality
- 0.1% dev fee for large miners
    - calc hashrate (wildly inaccurate :\)
- public-pool compatible api?
- better client difficulty adjustment? (interested? see `StratumClient.adjustDiffRoutine`)
- special bitaxe functions like auto-restart on flatline and hashrate polling?
- option to turn off vardiff?
- let clients version roll until range is exhausted? (protocol extension?)
- extranonce subscription?
- variable extranonce size?
- longpoll?
- more config options?
- more fine-grained client errors?
- stratum keepalive?
- zmq for block notifs?
