## Main test runner for Patroni Nim tests.
##
## Compile and run with:
##   nim c -r tests/all_tests.nim
##
## Or run individual test files:
##   nim c -r tests/test_utils.nim

# Import all test modules
import test_utils
import test_async_executor
import test_cancellable
import test_file_perm
import test_aws
import test_wale_restore
import test_barman
import test_etcd
import test_etcd3
import test_consul
import test_zookeeper
import test_kubernetes
import test_exhibitor
import test_raft
import test_raft_controller
import test_config
import test_config_generator
import test_postgresql
import test_postmaster
import test_rewind
import test_slots
import test_sync
import test_bootstrap
import test_ha
import test_api
import test_ctl
import test_callback_executor
import test_citus
import test_mpp
import test_log
import test_patroni
import test_quorum
import test_validator
import test_watchdog

echo "All Patroni tests completed."
