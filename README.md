# wrt54gl-sensor-collector

Prometheus exporter for a home telemetry hack, in which an Arduino board
was driving two sensor buses of Dallas 1-Wire thermal probes and a DHT22
temperature/humidity sensor.  The arduino was piggybacked on the WRT54GL
mostly to convert a TTL serial protocol into something accessible over
TCP/IP.  The exporter then takes the TCP/IP stream and exports it to
Prometheus.

This is likely of no interest to anyone else and is not a hardware design
that should be repeated -- it's just the parts I had on hand while in
a rush to get it done before the insulation contractors arrived and
covered it all up forever.

With luck the code will be useful as an example to someone, somewhere.  :)

