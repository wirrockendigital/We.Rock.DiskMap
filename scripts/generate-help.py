#!/usr/bin/env python3
"""Generate the offline DSM handbook from the embedded help catalogue."""
import argparse
import html
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CHAPTERS = [
    ('index', ['window'], 'Übersicht', 'Overview'),
    ('scans', ['scan', 'snapshots'], 'Scannen & Aktualisieren', 'Scanning & refreshing'),
    ('views', ['navigation', 'filestation', 'measurement'], 'Karten & Navigation', 'Maps & navigation'),
    ('filter', ['filters'], 'Suchen & Sortieren', 'Searching & sorting'),
    ('index-management', ['categories', 'snapshot-report', 'storage'], 'Index & Kategorien', 'Index & categories'),
    ('access-support', ['profiles', 'recycle', 'photos', 'session', 'support'], 'Zugriff & Problemlösung', 'Access & troubleshooting'),
    ('settings', ['settings', 'configuration', 'scheduler', 'recovery'], 'Einstellungen & Betrieb', 'Settings & operations'),
]


def tag(name, text, attrs=''):
    """Escape all authored text before producing static markup."""
    return f'<{name}{attrs}>{html.escape(text)}</{name}>'


def render_section(section, language):
    """Use the same accessible section and diagram semantics as the React reader."""
    result = '<section' + (' class="dm-help-warning"' if section.get('tone') == 'warning' else '') + '>'
    result += tag('h3', section['title'])
    result += ''.join(tag('p', value) for value in section.get('paragraphs', []))
    for key, element in [('steps', 'ol'), ('items', 'ul')]:
        if key in section:
            result += f'<{element}>' + ''.join(tag('li', value) for value in section[key]) + f'</{element}>'
    if 'diagram' in section:
        result += '<figure class="dm-help-diagram">' + tag('figcaption', 'Auf einen Blick' if language == 'de' else 'At a glance') + '<div>'
        result += ''.join(f'<span><b>{i+1}</b>{html.escape(value)}</span>' for i, value in enumerate(section['diagram']))
        result += '</div></figure>'
    return result + '</section>'


def outputs():
    """Keep chapter links and localized content in one deterministic build graph."""
    catalogue = json.loads((ROOT / 'web/src/features/settings/help-topics.json').read_text())
    files = {}
    for language, folder in [('de', 'ger'), ('en', 'enu')]:
        topics = {topic['id']: topic for topic in catalogue[language]}
        for slug, ids, german, english in CHAPTERS:
            title = german if language == 'de' else english
            nav = '<nav class="help-sidebar dm-help-nav" aria-label="' + ('Hilfethemen' if language == 'de' else 'Help topics') + '"><strong>We.Rock.DiskMap</strong><p>' + ('Handbuch' if language == 'de' else 'User guide') + '</p>'
            for other, children, de, en in CHAPTERS:
                nav += '<details open><summary>' + html.escape(de if language == 'de' else en) + '</summary><ul>'
                for child in children:
                    current = ' aria-current="page"' if other == slug else ''
                    nav += '<li><a href="' + other + '.html#' + child + '"' + current + '>' + html.escape(topics[child]['title']) + '</a></li>'
                nav += '</ul></details>'
            nav += '</nav>'
            body = ''
            for index, topic_id in enumerate(ids):
                topic = topics[topic_id]
                body += '<article id="' + topic_id + '">' + tag('p', title, ' class="dm-help-eyebrow"') + tag('h1' if index == 0 else 'h2', topic['title'])
                body += ''.join(tag('p', text, ' class="dm-help-lead"') for text in topic['paragraphs'])
                body += ''.join(render_section(section, language) for section in topic['sections']) + '</article>'
            files[f'{folder}/{slug}.html'] = '<!doctype html>\n<html lang="' + language + '"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>' + html.escape(title) + ' · We.Rock.DiskMap</title><link rel="stylesheet" href="../style.css"></head><body><div class="dm-help">' + nav + '<main class="dm-help-content">' + body + '</main></div></body></html>\n'
    files['style.css'] = '/* Native DSM help uses the same scoped handbook styling as the app. */\nbody{margin:0;padding:32px;background:#1e2b3e}body>.dm-help{max-width:1220px;margin:auto}@media(max-width:760px){body{padding:16px}}\n' + (ROOT / 'web/src/features/settings/help.css').read_text()
    return files


def main():
    """Check committed output in validation; generate it only when explicitly invoked."""
    parser = argparse.ArgumentParser()
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    for relative, content in outputs().items():
        path = ROOT / 'packaging/ui/help' / relative
        if args.check:
            if not path.is_file() or path.read_text() != content:
                raise SystemExit('Help output differs: ' + relative)
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)


if __name__ == '__main__':
    main()
