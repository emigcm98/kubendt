import React from 'react';

// The two optional "also delete…" checkboxes shared by the clear-topology and
// delete-namespace confirms, so both render identically. Styling comes from
// .alert-modal-extra (they're passed as AlertModal extraContent).
function DeleteExtrasCheckboxes({
  positionsChecked,
  onPositionsChange,
  positionsLabel = 'Also delete saved node positions',
  filesChecked,
  onFilesChange,
}) {
  return (
    <>
      <label>
        <input
          type="checkbox"
          checked={positionsChecked}
          onChange={(e) => onPositionsChange(e.target.checked)}
        />
        {positionsLabel}
      </label>
      <label>
        <input
          type="checkbox"
          checked={filesChecked}
          onChange={(e) => onFilesChange(e.target.checked)}
        />
        Also delete namespace files (file manager)
      </label>
    </>
  );
}

export default DeleteExtrasCheckboxes;
