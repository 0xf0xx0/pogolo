solo db-less bitcoin-only mining pool  
meant for lan swarms, not the internet  
think of this as public-pool but for self-sovereign nerds  
infinite thanks to btcd for bitcoin tooling and public-pool for reference  
extremely minimal, start it, point your miners to it, and watch the logs roll by

# setup
## install
```
go install github.com/0xf0xx0/pogolo@latest
```

## run
```
pogolo
```

## server config
see [pogolo.example.toml](./pogolo.example.toml)  
default path is $XDG_CONFIG_HOME/pogolo/pogolo.toml

## miner config
```
host: stratum+tcp://<your lan ip>:5661
username: <your address>[.<worker name>]
password: <optional, ignored by default>
```

## supported
- *axe family
- cpuminer
## unsupported
- miners needing extranonce.subscribe

TODO
- full test suite (almost done)
- public-pool compatible api
- 0.1% dev fee for large miners
    - calc hashrate
- document code for future viewers
- logs with personality
- colorize logs
- better client difficulty adjustment? (interested? see `StratumClient.adjustDiffRoutine`)
- share replay mitigation?
- multiple active jobs?
- extranonce subscription?
- variable extranonce size?
- stratum keepalive?
- longpoll?
- zmq for block notifs?
- client.reconnect on server restart?
