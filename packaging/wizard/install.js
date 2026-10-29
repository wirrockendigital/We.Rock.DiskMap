/* global SYNO, _S */
// Package Center displays the access model before installation or upgrade.
(function () {
  var de = typeof _S === 'function' && _S('lang') === 'ger';
  var text = de ? {
    title: 'Zugriff und Scanberechtigungen',
    access: 'We.Rock.DiskMap kann ausschließlich von DSM-Administratoren genutzt werden. Alle Administratoren verwenden einen gemeinsamen Scanbestand.',
    scan: 'Scans erfolgen mit dem eingeschränkten Paketkonto „we-rock-diskmap“. Dieses benötigt Leserechte für die gewünschten Freigaben. Die persönlichen Dateiberechtigungen des angemeldeten Administrators bestimmen den Scanumfang nicht.',
    permissions: 'Weise dem internen Systembenutzer we-rock-diskmap in den Berechtigungen der gewünschten freigegebenen Ordner Leserechte zu.',
    sources: 'Der Scan erfasst Dateinamen, Größen und die Verzeichnisstruktur. Die Quelldateien bleiben beim Scannen unverändert.'
  } : {
    title: 'Access and scan permissions',
    access: 'We.Rock.DiskMap is available exclusively to DSM administrators. All administrators share the same scan data.',
    scan: 'Scans run as the restricted we-rock-diskmap package account. Grant this account read access to the intended shares. The signed-in administrator’s personal file permissions do not determine the scan scope.',
    permissions: 'Grant the internal system user we-rock-diskmap read access in the permissions of the intended shared folders.',
    sources: 'Scanning records file names, sizes and the directory structure. Source files remain unchanged during scanning.'
  };
  var component = {
    props: SYNO.SDS.PkgManApp.Custom.useHook.props,
    setup: function (props) {
      var hooks = SYNO.SDS.PkgManApp.Custom.useHook(props);
      return {
        getNext: hooks.getNext,
        checkState: function (owner) { hooks.checkState(owner || props.getOwner()); },
        headline: text.title,
        getValues: function () { return {}; }
      };
    },
    render: function (h) {
      return h('pkg-center-step-content', [h('p', text.access), h('p', text.scan), h('p', text.permissions), h('p', text.sources)]);
    }
  };
  // Keep the same documented JSONP export as the existing uninstall wizard.
  SYNO.SDS.PkgManApp.Custom.JsonpLoader.load({default: {name: 'diskmap_access', render: component}});
}());
