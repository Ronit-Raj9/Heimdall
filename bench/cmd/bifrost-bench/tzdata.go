package main

// Embed the zone data so schedule, watch and drift monitor zones load
// whatever zone files the container ships.
import _ "time/tzdata"
