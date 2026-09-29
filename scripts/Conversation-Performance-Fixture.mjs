// Synthetic records with the shape and size of a long hardware debugging chat.
// No user messages, serial data, credentials or model calls are used.
export function conversationStressDetail(count=4800){
 const task={id:'long-chat-fixture',title:'长任务输入测试',workspace:'C:/fixture',environment:{id:'fixture',name:'本机测试',type:'windows'},engine:'codex',model:'fixture',reasoning_effort:'',session:'fixture-session',status:'done',archived:false,updated:1};
 const runs=[],events=[];
 for(let start=0;start<count;start+=200){
  const id='run-'+runs.length,result='测试完成。记录完整保留，可继续下一轮。';
  runs.push({id,kind:'chat',status:'done',result,error:'',source:'web',created:1000+start,started:1000+start,finished:2000+start});
  for(let i=start;i<Math.min(start+200,count);i++){
   const kind=i===start?'user':i===start+199?'assistant':'tool';
   events.push({seq:i+1,run_id:id,kind,text:kind==='user'?'检查模拟设备':kind==='assistant'?result:'模拟工具输出 '+i+'\n'+('sample output without private data\n'.repeat(34)),created:1000+i});
  }
 }
 return {task,runs,events,approvals:[],chat:''};
}
