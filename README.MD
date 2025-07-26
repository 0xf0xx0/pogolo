solo db-less ui-less bitcoin-only mining pool  
meant for lan swarms, not the internet  
think of this as public-pool but for self-sovereign nerds  
infinite thanks to btcd for bitcoin tooling and public-pool for reference  
extremely minimal, start it, point your miners to it, and watch the logs roll by

# setup
```
username: <your address>[.<worker name>]
password: <optional, ignored by default>
```

## supported
- *axe family
- cpuminer
## unsupported
- miners needing extranonce.subscribe

TODO
- proper error handling (reduce panics)
- full test suite
- document code for future viewers
- client difficulty adjustment
- 1% dev fee for large miners
- share replay mitigation
- stratum keepalive?
- public-pool compatible api?
- client.reconnect on server restart?
