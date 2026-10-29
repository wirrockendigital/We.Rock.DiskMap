/* global SYNO, Vue */
// Use DSM's documented Vue application components; React stays inside its frame.
SYNO.namespace('SYNO.SDS.App.DiskMap');
SYNO.SDS.App.DiskMap.Instance = Vue.extend({
  render: function (h) {
    return h('v-app-instance', {
      props: { className: 'SYNO.SDS.App.DiskMap.Instance' }
    }, [h('v-app-window', {
      ref: 'appWindow',
      attrs: { 'syno-id': 'WeRockDiskMapWindow' },
      // Open bundled help directly: DSM's online catalogue omits private packages.
      props: { width: 1000, height: 680, resizable: true, showHelp: true,
        onHelp: function () { SYNO.SDS.AppLaunch('SYNO.SDS.App.DiskMap.HelpInstance'); }
      }
    }, [h('iframe', {
      attrs: {
        title: 'We.Rock.DiskMap',
        src: '/webman/3rdparty/we-rock-diskmap/index.html'
      },
      style: { display: 'block', width: '100%', height: '100%', border: '0' }
    })])]);
  }
});

// A single native DSM window keeps local help independent of the online catalogue.
SYNO.SDS.App.DiskMap.HelpInstance = Vue.extend({
  render: function (h) {
    var language = SYNO.SDS.Utils.getHelpLanguage() === 'ger' ? 'ger' : 'enu';
    return h('v-app-instance', {
      props: { className: 'SYNO.SDS.App.DiskMap.HelpInstance' }
    }, [h('v-app-window', {
      props: { width: 800, height: 660, resizable: true, showHelp: false }
    }, [h('iframe', {
      attrs: {
        title: language === 'ger' ? 'We.Rock.DiskMap – Hilfe' : 'We.Rock.DiskMap – Help',
        src: '/webman/3rdparty/we-rock-diskmap/help/' + language + '/index.html'
      },
      style: { display: 'block', width: '100%', height: '100%', border: '0' }
    })])]);
  }
});
