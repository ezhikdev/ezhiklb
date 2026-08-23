# Changelog

## 0.1.0-alpha.5

- Replaced expanded listener cards with compact Cloudflare-style rule rows.
- Added focused rule editing, cloning, delete confirmation and unsaved-change protection.
- Added client/server validation for conflicting listeners and duplicate backends.
- Added calculated backend weight percentages and inline ICMP health state.
- Added persisted per-service and per-backend IPVS counters to the Overview page.
- Added explicit applying/applied/error node states.
- Reduced heartbeat write frequency to 15 seconds and added revision ETags.
- Improved upgrade backups and rollback data restoration.
- Added safe cache headers so panel upgrades load the current frontend.

## 0.1.0-alpha.4

- Fixed listener creation on panels opened over plain HTTP.

## 0.1.0-alpha.3

- Fixed executable directory permissions and installer version detection.
