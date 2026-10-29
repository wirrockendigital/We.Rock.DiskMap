"""Validate the native help registration and every bundled localized chapter."""
import configparser
from html.parser import HTMLParser
import json
from pathlib import Path
import unittest
import subprocess

UI = Path(__file__).resolve().parents[2] / 'packaging/ui'


class References(HTMLParser):
    """Collect resources and links without executing help markup."""

    def __init__(self):
        super().__init__()
        self.links = []
        self.scripts = []

    def handle_starttag(self, tag, attrs):
        values = dict(attrs)
        if tag == 'script':
            self.scripts.append(values)
        for name in ('href', 'src'):
            if name in values:
                self.links.append(values[name])


class NativeHelpTests(unittest.TestCase):
    """Catch broken DSM topic identity, translations and offline links before packaging."""

    def test_generated_help_matches_shared_catalogue(self):
        subprocess.run(['python3', str(UI.parents[1] / 'scripts/generate-help.py'), '--check'], check=True)

    def test_localized_registration_and_offline_resources(self):
        toc = json.loads((UI / 'helptoc.conf').read_text())
        apps = json.loads((UI / 'config').read_text())['DiskMap.js']
        self.assertIn(toc['app'], apps)
        self.assertEqual(apps[toc['app']]['appWindow'], toc['app'])
        topics = [toc, *toc['toc']]
        self.assertEqual(len({t['content'] for t in topics}), len(topics))
        for language in ('ger', 'enu'):
            strings = configparser.ConfigParser()
            strings.read(UI / 'texts' / language / 'strings')
            for topic in topics:
                section, key = topic['title'].split(':')
                self.assertTrue(strings[section][key].strip('"'))
                path = UI / 'help' / language / topic['content']
                markup = path.read_text()
                self.assertIn('<h1>', markup)
                # Every native chapter retains the same offline, collapsible topic tree.
                self.assertIn('class="help-sidebar dm-help-nav"', markup)
                self.assertEqual(markup.count('<details open>'), len(topics))
                self.assertIn('aria-current="page"', markup)
                if path.name == 'access-support.html':
                    self.assertIn('we-rock-diskmap', markup)
                    self.assertIn('#recycle', markup)
                    self.assertIn('<ol>', markup)
                    # The documented checkbox coupling must survive catalogue edits.
                    for phrase in (('entfernt DSM automatisch den Haken', 'entzieht DSM dem App-Benutzer') if language == 'ger' else ('automatically', 'access')):
                        self.assertIn(phrase, markup)
                parser = References()
                parser.feed(markup)
                self.assertFalse(parser.scripts)
                for link in parser.links:
                    self.assertNotIn('://', link)
                    filename, _, anchor = link.partition('#')
                    target = (path.parent / filename).resolve()
                    if anchor:
                        self.assertIn('id="' + anchor + '"', target.read_text())
                    self.assertTrue(target.is_relative_to((UI / 'help').resolve()))
                    self.assertTrue(target.is_file(), link)


if __name__ == '__main__':
    unittest.main()
