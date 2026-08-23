# Profile editor override

Profiles use progressive disclosure for routing rules:

- The profile surface shows compact rule rows, never expanded backend forms.
- A row summarizes name, protocols, scheduler, ingress, backend count and effective weight percentages.
- Clicking the row or its pencil opens a focused rule dialog.
- Enable/disable remains available directly from the row.
- Clone and delete are explicit icon actions with accessible labels; delete requires confirmation.
- Field errors appear beside the invalid value. Conflicts include wildcard listen addresses.
- Closing a dirty rule or profile requires confirmation.
- Live health and traffic data are visually secondary to editable desired state.
