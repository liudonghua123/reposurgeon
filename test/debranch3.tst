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
set flag echo
:17 list paths
:15 list paths
:13 list paths
:11 list paths
:9 list paths
:6 list paths
:4 list paths
:2 list paths
:9 list inspect
:15 list inspect
:6 list inspect
