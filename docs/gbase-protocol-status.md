# GBase source protocol status

The full registry contains separate GBase 8s and 8t source types. Both are
unavailable for connection in this build until product-specific native driver
and server-version contracts are verified. Existing configurations using
these types will receive an unavailable error; they are not silently routed to
a MySQL endpoint. This is a change from the previous unverified routing.

The vendor documents a GBase 8s JDBC connection using
[the gbasedbt-sqli driver even in its MySQL SQL-compatibility mode](https://www.gbase.cn/community/post/11902).
Its [8s ODBC setup guide](https://www.gbase.cn/news/3243) also describes
GBase 8t client settings. These examples do not establish every release's
wire protocols, nor do they verify that the Go MySQL adapter supports either
product. SQL syntax compatibility is not proof of wire compatibility.

Re-enabling either type requires a product-specific adapter, a pinned native
connection and read-only query contract, documented configuration and accurate
catalog reporting. Do not infer support for the other type or other versions
from one successful contract.
