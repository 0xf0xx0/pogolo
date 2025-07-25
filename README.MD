solo db-less ui-less bitcoin-only mining pool  
meant for lan, not the internet  
infinite thanks to btcd for bitcoin tooling and public-pool  
extremely minimal, start it, point your miners to it, and watch the logs roll by

# setup
```
username: <your address>[.<worker name>]
password: <ignored>
```

## supported
- *axe family
- cpuminer
## unsupported
- miners needing extranonce.subscribe

TODO
- proper error handling (reduce panics)
- client difficulty adjustment
- share replay mitigation
- stratum keepalive?
- public-pool compatible api?
- client.reconnect on server restart?
