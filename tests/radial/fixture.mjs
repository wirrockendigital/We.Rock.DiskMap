// Synthetic shared hierarchy used only for deterministic renderer checks.
export function radialFixture(){
 const s=(b,f,d)=>({knownBytes:String(b),unknownFileCount:'0',fileCount:String(f),directoryCount:String(d),specialCount:'0'});
 const n=(id,parentId,kind,summary)=>({id,parentId,kind,name:id==='root'?'Recorded root / aufgezeichneter Umfang mit langem Namen':id,virtual:false,realNodeId:id,summary,originalSummary:summary,matchSummary:summary,matchKind:'direct',hasMore:false,classification:null,colorBranchKey:'cb1-'+(parentId==='folder'?'b':'a').repeat(64)});
 const nodes=[n('root','external','directory',s(1020,22,2)),n('large','root','file',s(900,1,0)),n('folder','root','directory',s(100,1,1)),n('nested','folder','file',s(100,1,0))];
 for(let i=0;i<20;i++)nodes.push(n('small-'+String(i).padStart(2,'0'),'root','file',s(1,1,0)));
 return {snapshotId:'synthetic',rootId:'root',metric:'logical',mode:'highlight',state:'complete',selectionOrder:'recorded-rowid',nodes,truncated:false,filter:{categories:[]},matchState:'matches',originalSummary:nodes[0].summary,matchSummary:nodes[0].summary,denominator:nodes[0].summary,coverage:[],sourceAvailability:{total:'0',truncated:false,records:[]}};
}

// A real eight-level chain verifies that center content cannot spill into narrow rings.
export function deepRadialFixture(){
 const wire=radialFixture(),sample=wire.nodes[0];
 wire.nodes=Array.from({length:9},(_,i)=>{const summary={knownBytes:'100',unknownFileCount:'0',fileCount:'1',directoryCount:String(Math.max(0,8-i)),specialCount:'0'};return {...sample,id:i===0?'root':'depth-'+i,parentId:i===0?'external':i===1?'root':'depth-'+(i-1),realNodeId:i===0?'root':'depth-'+i,kind:i===8?'file':'directory',name:i===0?'Very long recorded root name / tiefer Umfang':'Depth '+i,summary,originalSummary:summary,matchSummary:summary};});
 return {...wire,originalSummary:wire.nodes[0].summary,matchSummary:wire.nodes[0].summary,denominator:wire.nodes[0].summary};
}
