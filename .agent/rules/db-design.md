---
trigger: always_on
---

---
This is a file to describe db design principles.
---

- No use foreign key. (In modern architectures especially distributed system, foreign key is a burden.)
- Always use snake_style for field name.
