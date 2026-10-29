/* global SYNO, Vue, _S */
// DSM supplies Vue and its wizard hooks; no framework is bundled with this component.
(function () {
  var de = typeof _S === 'function' && _S('lang') === 'ger';
  var text = de ? {
    title: 'We.Rock.DiskMap deinstallieren',
    keep: 'Standard: Eigene Konfiguration, Profile, Indexdaten und lokale Protokolle behalten. Eine Neuinstallation verwendet diese Daten wieder.',
    remove: 'Eigene DiskMap-Daten vollständig entfernen',
    details: 'Bei Auswahl werden Konfiguration, Profile, Indexdaten, Upgrade-Sicherungen und lokale Protokolle gelöscht. Die leere Koordinationsdatei bleibt erhalten. Stoppe das Paket vor dieser Auswahl im Paket-Zentrum. Sichere benötigte Daten vorher konsistent; ein abgebrochener Löschlauf muss vor einem Neustart beendet werden.',
    sources: 'Analysierte Freigaben, NAS-Dateien und deren Zugriffsrechte bleiben unverändert.',
    tasks: 'Selbst eingerichtete DSM-Aufgaben bleiben bestehen. Prüfe im Aufgabenplaner alle DiskMap-Aufrufe und ihre --profile-id-Zuordnung. Deaktiviere oder entferne nicht mehr benötigte Aufgaben selbst. Nach Entfernung der Profildaten startet eine alte ID kein neu angelegtes Ersatzprofil.',
    keepTasks: 'Bei beibehaltenen Daten können bestehende Aufgaben nach Neuinstallation wieder dasselbe erhaltene Profil aufrufen. Kopieren oder Importieren anderer Profile ist keine automatische Zuordnung.'
  } : {
    title: 'Uninstall We.Rock.DiskMap',
    keep: 'Default: Keep application configuration, profiles, indexes and local logs. Reinstallation reuses these data.',
    remove: 'Remove all DiskMap application data',
    details: 'Selecting this deletes configuration, profiles, indexes, upgrade backups and local logs. The empty coordination file remains. Stop the package in Package Center before selecting this option. Make a consistent backup of needed data first; interrupted removal must finish before the service can restart.',
    sources: 'Analyzed shares, NAS files and their access permissions remain unchanged.',
    tasks: 'User-created DSM tasks remain. Review all DiskMap commands and their --profile-id references in Task Scheduler. Disable or remove obsolete tasks yourself. After removing profile data, an old ID cannot start a newly created replacement profile.',
    keepTasks: 'When retaining data, existing tasks can invoke the same retained profile after reinstallation. Copying or importing other profiles is not an automatic reassignment.'
  };
  var component = {
    props: SYNO.SDS.PkgManApp.Custom.useHook.props,
    setup: function (props) {
      var hooks = SYNO.SDS.PkgManApp.Custom.useHook(props);
      var remove = Vue.ref(false);
      return {
        getNext: hooks.getNext,
        checkState: function (owner) { hooks.checkState(owner || props.getOwner()); },
        headline: text.title,
        // Package Center merges named fields; match the official ExamplePackage object contract.
        getValues: function () { return {diskmap_remove_data: remove.value}; },
        remove: remove
      };
    },
    render: function (h) {
      var self = this;
      return h('pkg-center-step-content', [
        h('p', text.keep),
        h('label', [h('input', {attrs: {type: 'checkbox'}, domProps: {checked: self.remove}, on: {change: function (e) { self.remove = e.target.checked; }}}), text.remove]),
        h('p', text.details), h('p', text.sources), h('p', text.tasks), h('p', text.keepTasks)
      ]);
    }
  };
  // Match the documented JSONP library's default module export contract.
  SYNO.SDS.PkgManApp.Custom.JsonpLoader.load({default: {name: 'diskmap_remove', render: component}});
}());
