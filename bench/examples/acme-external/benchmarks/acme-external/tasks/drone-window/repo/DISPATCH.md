# Acme Freight dispatch rules

Drone deliveries fly from a depot in a slot only when the forecast for that
slot at that depot has:

- wind gusts below 35 km/h, and
- a probability of precipitation below 50 percent.

Otherwise the depot holds its drones for the slot and the parcels go by van.
The morning slot is 09:00.
