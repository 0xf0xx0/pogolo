#!/bin/bash
set -eu

host="[::1]"
port="5661"
addr=""
# assumes 8 thread cpu and cpuminer-opt
./cpuminer -o "$host:$port" -a sha256d --cpu-priority 5 --cpu-affinity 0x1 -q -t 1 -O "$addr.benchy1:" > /dev/null &
./cpuminer -o "$host:$port" -a sha256d --cpu-priority 5 --cpu-affinity 0x4 -q -t 1 -O "$addr.benchy2:" > /dev/null &
./cpuminer -o "$host:$port" -a sha256d --cpu-priority 5 --cpu-affinity 0x10 -q -t 1 -O "$addr.benchy3:" > /dev/null &
./cpuminer -o "$host:$port" -a sha256d --cpu-priority 5 --cpu-affinity 0x40 -q -t 1 -O "$addr.benchy4:" > /dev/null &
#./cpuminer -o "$host:$port" -a sha256d --cpu-priority 5 --cpu-affinity 0x100 -q -t 1 -O "$addr.benchy5:" > /dev/null &

wait
