# kim_network

Manages a Project-owned logical Network. `profile`, `segment_policy`, and an explicit requested segment are replacement fields. Allocated segment and OVN backend identities are KIM-owned and absent from state.

Create, mutable revision updates, import (`network/<uuid>`), and destroy poll the KIM Operation until verified convergence.
