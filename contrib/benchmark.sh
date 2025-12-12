#!/bin/bash
set -eu

host="[::1]"
port="5661"
# assumes 8 thread cpu
./cpuminer -o "$host:$port" -a sha256d --cpu-priority 5 --cpu-affinity 0x1 -q -t 1 -O "benchy1:" &
./cpuminer -o "$host:$port" -a sha256d --cpu-priority 5 --cpu-affinity 0x4 -q -t 1 -O "benchy2:" &
./cpuminer -o "$host:$port" -a sha256d --cpu-priority 5 --cpu-affinity 0x10 -q -t 1 -O "benchy3:" &
./cpuminer -o "$host:$port" -a sha256d --cpu-priority 5 --cpu-affinity 0x40 -q -t 1 -O "benchy4:" &
#./cpuminer -o "$host:$port" -a sha256d --cpu-priority 5 --cpu-affinity 0x100 -q -t 1 -O "benchy5:" &

wait
