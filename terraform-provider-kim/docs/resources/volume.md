# kim_volume

Manages a backend-neutral logical Volume. KIM chooses exact current capacity/backend authority and verifies materialization before Operation success. Backend, Host, VG/LV UUID, binding, and capacity claim identities are not state.

Size, Storage Class, boot/source identity are replacement fields. Import format: `volume/<uuid>`.
