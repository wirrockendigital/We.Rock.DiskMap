import {useId} from 'react';
import catalogue from './help-topics.json';
export type HelpLanguage='de'|'en';
export interface HelpSection {title:string;paragraphs?:string[];steps?:string[];items?:string[];diagram?:string[];tone?:string}
export interface HelpTopic {id:string;title:string;paragraphs:readonly string[];sections:HelpSection[]}
// Native DSM help and the embedded reader share this local, translated source.
export function helpTopics(language:HelpLanguage):readonly HelpTopic[]{return catalogue[language];}
// Semantic diagrams remain readable without images, network access or scripting.
export function OfflineHelpContent({language}:{language:HelpLanguage}){
 const prefix=useId(),topics=helpTopics(language),de=language==='de';
 const groups=[{title:de?'Start & Bedienung':'Getting started',ids:['window','scan','navigation','filters','filestation','measurement']},{title:de?'Zugriff & Daten':'Access & data',ids:['profiles','recycle','categories','snapshot-report','snapshots','storage']},{title:de?'Einstellungen & Hilfe':'Settings & support',ids:['settings','support','session','configuration','scheduler','recovery']}];
 return <div className="dm-help" lang={language}>
  <nav className="dm-help-nav" aria-label={de?'Hilfethemen':'Help topics'}><strong>We.Rock.DiskMap</strong><p>{de?'Handbuch':'User guide'}</p>{groups.map(group=><details key={group.title} open><summary>{group.title}</summary><ul>{group.ids.map(id=><li key={id}><a href={'#'+prefix+'-'+id}>{topics.find(t=>t.id===id)!.title}</a></li>)}</ul></details>)}</nav>
  <div className="dm-help-content">{topics.map(topic=><article id={prefix+'-'+topic.id} key={topic.id}><p className="dm-help-eyebrow">We.Rock.DiskMap · {de?'Handbuch':'User guide'}</p><h2>{topic.title}</h2>{topic.paragraphs.map((text,i)=><p className="dm-help-lead" key={i}>{text}</p>)}{topic.sections.map((section,i)=><section key={i} className={section.tone==='warning'?'dm-help-warning':undefined}><h3>{section.title}</h3>{section.paragraphs?.map((text,j)=><p key={j}>{text}</p>)}{section.steps&&<ol>{section.steps.map((text,j)=><li key={j}>{text}</li>)}</ol>}{section.items&&<ul>{section.items.map((text,j)=><li key={j}>{text}</li>)}</ul>}{section.diagram&&<figure className="dm-help-diagram"><figcaption>{de?'Auf einen Blick':'At a glance'}</figcaption><div>{section.diagram.map((text,j)=><span key={j}><b>{j+1}</b>{text}</span>)}</div></figure>}</section>)}</article>)}</div>
 </div>;
}
