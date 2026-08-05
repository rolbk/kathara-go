# PROPOSED-DIVERGENCES

Redesign ideas outside the §0.2 sanctioned list. Not implemented. A human decides, possibly after 1.0.

## Deterministic MACs via kathara.machine driver opt
The network plugin derives deterministic MACs only when the `kathara.machine`+`kathara.iface` driver opts are sent; Kathara 3.8.3 sends `kathara.iface`+`kathara.link`, so default-path MACs are random per deploy. Adding `kathara.machine` would make MACs deterministic and match the (incorrect) claim in PORT_SPEC §9, but changes on-the-wire behaviour vs 3.8.3. Not implemented.

## FolderParser machine order (OQ-15a ruling applied)
Python's conf-less machine order is `glob` order = readdir order (ext4 hash order, effectively random per filesystem). Go sorts names. Any fixed order is within Python's observable envelope; vectors are marked order-insensitive. Recorded here because it is technically a behaviour change on any single given filesystem.
