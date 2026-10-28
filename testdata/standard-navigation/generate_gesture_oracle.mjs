import fs from 'node:fs';import crypto from 'node:crypto';import {createRequire} from 'node:module';
const require=createRequire(new URL('../../web/package.json',import.meta.url)),ts=require('typescript'),vue=require('vue');
const source='/Users/accidia/projects/MoviePilot-Frontend/src/composables/usePullDownGesture.ts',raw=fs.readFileSync(source,'utf8'),tree=ts.createSourceFile('fixed.ts',raw,ts.ScriptTarget.Latest,true);
const chosen=tree.statements.filter(n=>ts.isFunctionDeclaration(n)&&n.name.text==='usePullDownGesture'||ts.isVariableStatement(n)&&n.declarationList.declarations.some(d=>d.name.getText(tree)==='DEFAULT_CONFIG'));
const code=ts.transpileModule(chosen.map(n=>n.getText(tree).replace(/^export /,'')).join('\n'),{compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText;
const inputs=[{name:'trigger-at-resisted120',moves:[{y:160}]},{name:'indicator60-below-trigger',moves:[{y:80}]},{name:'continued-small-pull-keeps-resistance',moves:[{y:160},{y:10}]},{name:'negative80-cancels-current-pull',moves:[{y:160},{y:-80}]},{name:'initial-scroll101-blocks',initialScroll:101,moves:[{y:160,scroll:0}]},{name:'continued-pull-ignores-later-scroll',moves:[{y:160},{y:300,scroll:200}]},{name:'dialog-refresh-after500-cancels',moves:[{y:160},{y:180,overlay:true,elapsed:501}]}];
const cases=[];
for(const input of inputs){let now=1000,scroll=input.initialScroll||0,overlay=false,triggers=0,prevented=0;const events={},timers=[];
 const globals={ref:vue.ref,computed:vue.computed,readonly:vue.readonly,watch:vue.watch,onMounted:fn=>fn(),onBeforeUnmount(){},useDisplay:()=>({mdAndDown:{value:true}}),usePWA:()=>({appMode:vue.ref(true)}),document:{documentElement:{scrollTop:0},querySelectorAll:()=>overlay?[{}]:[],addEventListener(k,fn){events[k]=fn},removeEventListener(){}},window:{get scrollY(){return scroll}},Date:{now:()=>now},setTimeout(fn,delay){timers.push(delay)},console:{warn(){}}};
 const s=new Function(...Object.keys(globals),'onTrigger',code+';return usePullDownGesture({enabled:true,canUsePullGesture:()=>true,onTrigger})')(...Object.values(globals),()=>triggers++);
 events.touchstart({touches:[{clientY:0}]});
 for(const move of input.moves){now+=move.elapsed||1;if(move.scroll!==undefined)scroll=move.scroll;if(move.overlay!==undefined)overlay=move.overlay;events.touchmove({touches:[{clientY:move.y}],preventDefault(){prevented++}})}
 const before={distance:s.pullDistance.value,pulling:s.isPulling.value,transform:s.contentTransform.value,transition:s.contentTransition.value,indicator:s.showPullIndicator.value};events.touchend();
 cases.push({input,expected:{before,triggered:triggers,prevented,timer_ms:timers}});
}
fs.writeFileSync(new URL('./gesture-source-oracle.json',import.meta.url),JSON.stringify({fixed_commit:'2829e3b08214348f05ff2f5beae644882eccad58',source:'src/composables/usePullDownGesture.ts',sha256:crypto.createHash('sha256').update(raw).digest('hex'),kind:'fixed full gesture function with owned Vue/API/time/DOM boundaries',cases},null,2)+'\n');console.log('fixed gesture cases:',cases.length);
