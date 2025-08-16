const net = require('node:net')

const MOCK_ADDRESS                   = "bcrt1qv2w0jh49962fc0qw63aqlw6p567qkx2dj5kpg4"
const MOCK_MINING_SUBSCRIBE          = `{"id": 1, "method": "mining.subscribe", "params": ["bitaxe/FTXGOXX/v2021-08-24"]}\n`
const MOCK_MINING_CONFIGURE          = `{"id": 2, "method": "mining.configure", "params": [["version-rolling"], {"version-rolling.mask": "ffffffff"}]}\n`
const MOCK_MINING_AUTHORIZE          = `{"id": 3, "method": "mining.authorize", "params": ["${MOCK_ADDRESS}.fakeminer", "x"]}\n`
const conn = net.createConnection(5661)

conn.write(MOCK_MINING_AUTHORIZE)
conn.write(MOCK_MINING_CONFIGURE)
conn.write(MOCK_MINING_SUBSCRIBE)
conn.on('data', x => {
    console.log(x.toString())
})
