## Test file expunge operation
verbose 1
echo 1
quiet on
# There's a --nobranch embedded in the test load so it can be checked standalone.
# This invocation would make the load work even without that.
read --nobranch <expunge.svn
1..$ expunge /^releases\/v1.0\/.*/
choose