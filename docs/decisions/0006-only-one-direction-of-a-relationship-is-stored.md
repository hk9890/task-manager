# 0006: An issue stores its own relationships, and the inverse direction is computed

An issue stores its parent, its blockers and its related issues. Its children, the issues it
blocks and the issues that name it as related are computed by a scan and never written, so the
on-disk graph cannot contradict itself.
