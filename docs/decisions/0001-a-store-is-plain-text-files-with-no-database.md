# 0001: A store is a directory tree of plain text files, with no database and no daemon

Every file of a store is text that a person can read and a reviewer can diff line by line. A store
committed with its project is shared by the version control of that project. No index file exists
that must be kept in sync.

Rejected: a database or SQL backend, because it is unused weight.
