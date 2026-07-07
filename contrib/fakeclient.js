const net = require('node:net')

const MOCK_ADDRESS                   = "bcrt1qv2w0jh49962fcqw63aqlw6p567qkx2dj5kpg4"
const MOCK_MINING_SUBSCRIBE          = `{"id": 1, "method": "mining.subscribe", "params": ["bitaxe/FTXGOXX/v2021-08-24"]}\n`
const MOCK_MINING_CONFIGURE          = `{"id": 2, "method": "mining.configure", "params": [["version-rolling"], {"version-rolling.mask": "ffffffff"}]}\n`
const MOCK_MINING_AUTHORIZE          = `{"id": 3, "method": "mining.authorize", "params": ["${MOCK_ADDRESS}.fakeminer", ""]}\n`
const MOCK_MINING_SUGGEST_DIFFICULTY = `{"id": 4, "method": "mining.suggest_difficulty", "params": [0.16]}\n`
const conn = net.createConnection(5661, process.env.STRATUM_HOST || '10.42.0.1')

conn.write(MOCK_MINING_SUBSCRIBE)
conn.write(MOCK_MINING_CONFIGURE)
conn.write(MOCK_MINING_AUTHORIZE)
conn.write(MOCK_MINING_SUGGEST_DIFFICULTY)

conn.on('data', x => {
    console.log(x.toString())
})
conn.on('error', x => {
    console.log(x.toString())
})
