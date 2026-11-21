# 0005: The engine allocates an issue ID as a random token, with no counter

Two git branches each allocate IDs without knowing of the other. A random token from about 2×10⁹
values means they effectively never pick the same ID, so no renumber tooling is required.

Rejected: sequential numbers, because parallel branches took the same number.

Rejected: a counter file, because it would be the worst git merge hotspot.
