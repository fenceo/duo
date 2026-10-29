// Split every matching viewport block while retaining cascade order. Feature
// styles may append their own media query after the shared mobile workbench.
export function viewportStyles(source,width=760){
 const css=source.replace(/\/\*[\s\S]*?\*\//g,''),pattern=new RegExp('@media\\s*\\(\\s*max-width\\s*:\\s*'+width+'px\\s*\\)\\s*\\{','g');
 const mobile=[],desktop=[];let cursor=0,match;
 while((match=pattern.exec(css))){
  desktop.push(css.slice(cursor,match.index));let depth=1,end=pattern.lastIndex,quote='';
  for(;end<css.length&&depth;end++){const c=css[end];if(quote){if(c==='\\')end++;else if(c===quote)quote=''}else if(c==='"'||c==="'")quote=c;else if(c==='{')depth++;else if(c==='}')depth--}
  if(depth)throw Error('Unclosed viewport CSS block');
  mobile.push(css.slice(pattern.lastIndex,end-1));cursor=end;pattern.lastIndex=end;
 }
 desktop.push(css.slice(cursor));if(!mobile.length)throw Error('Viewport CSS block not found: '+width);
 return {mobile:mobile.join('\n'),desktop:desktop.join('\n')};
}
