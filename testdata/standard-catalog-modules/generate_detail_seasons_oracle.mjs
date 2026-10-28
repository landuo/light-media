import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { writeFileSync } from 'node:fs';
import vm from 'node:vm';
const commit = '2829e3b08214348f05ff2f5beae644882eccad58';
const path = 'src/views/discover/MediaDetailView.vue';
const source = execFileSync('git', ['show', `${commit}:${path}`], { cwd: '/Users/accidia/projects/MoviePilot-Frontend', encoding: 'utf8' });
const start = source.indexOf('const getMediaSeasons = computed(() => {');
const end = source.indexOf('\n})', start) + 3;
if(start < 0 || end < start) throw Error('fixed season computed missing');
const code = source.slice(start, end) + '\ngetMediaSeasons';
const cases = [
 ['unordered', [{season_number:0,name:'Specials',episode_count:1},{season_number:2,name:'Two',episode_count:8},{season_number:1,name:'One',episode_count:3}]],
 ['null', null], ['empty', []], ['missing', undefined],
 ['missing_number', [{name:'Without number'},{season_number:2},{season_number:0}]],
 ['duplicate', [{season_number:2,name:'first'},{season_number:1},{season_number:2,name:'second'}]],
].map(([name, season_info]) => ({name, input: season_info === undefined ? {} : {season_info}, output:vm.runInNewContext(code, {mediaDetail:{value:{season_info}},computed:fn=>fn()})}));
writeFileSync(new URL('./detail-seasons-source-oracle.json', import.meta.url), JSON.stringify({commit,path,sha256:createHash('sha256').update(source).digest('hex'),start_line:source.slice(0,start).split('\n').length,code,cases}, null, 2)+'\n');
console.log(cases.length+' fixed frontend season cases');
