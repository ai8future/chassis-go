# Redpanda volume cleanup concurrency false failure

The selected Redpanda integration compared the entire Docker daemon volume
inventory with a preflight snapshot. Unrelated concurrent volume creation or
removal could therefore fail the suite even after its exact container and every
captured anonymous volume were removed successfully.

The cleanup assertion now filters the post-cleanup inventory by the exact
volume IDs captured from the selected Redpanda container. A deterministic
ownership-set regression and an adversarial live run prove unrelated volume
churn is ignored while any captured owned ID that remains still fails closed.
