# Record today's delivery

Delivery D-5531 from our supplier just arrived with 12 units of the 750 ml
steel water bottle. Record it in the inventory, and put the delivery number
in the reason so the warehouse can trace it. Do not change any other item.

The inventory service is an HTTP tool server (a GET on its address lists its
endpoints). An agent in the run's container reaches it at
http://toolproxy:9200/s/inventory/; an A2A agent gets its address and a
bearer token in this message's metadata, under heimdall.tools.inventory.
