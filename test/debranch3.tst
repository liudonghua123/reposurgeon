## Test path modification of debranch
#
# Here's the topology of debranch3.fi:
#
#         :2
#         |
#         :4
#         |
#         :6
#         |
#         +-----------+
#         |           |
#         :9          :15
#         |           |
#         :11         :17
#         |           |
#         :13      [master]
#         |
#     [alternate]
#
read <debranch3.fi
debranch alternate master
write -
set echo
:17 path list
:15 path list
:13 path list
:11 path list
:9 path list
:6 path list
:4 path list
:2 path list
:9 inspect
:15 inspect
:6 inspect
