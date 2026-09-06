# Shared Materialization Fixtures

These fixtures are synthetic, credential-free inputs for the shared materialization implementation tasks. They record the app shape M01 observed and the source files that motivated it, but they are not app adoption artifacts.

Each JSON file is intended to be consumed by later contract and integration tests as a stable example of one app-shaped input:

- `cairn-install.json`: authored composition, filesystem tree import and managed JSON/TOML install.
- `nanite-refresh.json`: immutable blob skill package, refresh and owned removal.
- `torque-loopback-task.json`: generated task tree plus task-scoped loopback requirement.
- `tether-multi-task-tree.json`: multiple Torque task bundles plus copied context tree and provider overlay.
