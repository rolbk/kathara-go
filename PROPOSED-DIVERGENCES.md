# PROPOSED-DIVERGENCES

Redesign ideas outside the §0.2 sanctioned list. Not implemented. A human decides, possibly after 1.0.

## Deterministic MACs via kathara.machine driver opt
The network plugin derives deterministic MACs only when the `kathara.machine`+`kathara.iface` driver opts are sent; Kathara 3.8.3 sends `kathara.iface`+`kathara.link`, so default-path MACs are random per deploy. Adding `kathara.machine` would make MACs deterministic and match the (incorrect) claim in PORT_SPEC §9, but changes on-the-wire behaviour vs 3.8.3. Not implemented.
